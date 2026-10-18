'use strict';
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {fork,spawn}=require('node:child_process');
const {IDS,validateConfig}=require('./config');
const {Telemetry}=require('./telemetry');
const {SessionWatcher}=require('./session-watcher');
const {randomBytes}=require('node:crypto');
const PORT=Number(process.env.SCREEN_HUB_PORT||19330),HOST='127.0.0.1',BASE=`http://${HOST}:${PORT}`;
if(!Number.isInteger(PORT)||PORT<1024||PORT>65535)throw Error('Invalid local port');
const DATA=path.join(__dirname,'data');fs.mkdirSync(DATA,{recursive:true});
const defaults=Object.fromEntries(IDS.map(id=>[id,{url:`${BASE}/screens/${id}`,fps:5,quality:80,rotation:id==='asus'?180:0}]));
let configs={...defaults};
try{const saved=JSON.parse(fs.readFileSync(path.join(DATA,'config.json'),'utf8'));for(const id of IDS)configs[id]=validateConfig(id,saved[id]||{},defaults[id]);}catch(error){if(error.code!=='ENOENT')console.error('Settings reset: '+error.message);}
const telemetry=new Telemetry(),clients=new Map(),sessionWatcher=new SessionWatcher(),sessionToken=randomBytes(32).toString('hex');let shuttingDown=false,sessionEnding=false,poweringOff;
const descriptors={aigo:{name:'爱国者长条屏',width:1920,height:462},asus:{name:'华硕水冷屏',width:320,height:320}};
class DisplayClient {
  constructor(id){
    this.id=id;this.serial=0;this.pending=new Map();this.state={id,...descriptors[id],running:false,connected:false,sent:0,bytes:0,actualFps:0,retries:0,error:'',config:configs[id]};
    this.ready=new Promise((resolve,reject)=>{this.resolveReady=resolve;this.rejectReady=reject;});this.ready.catch(()=>{});
    this.child=fork(path.join(__dirname,'display-worker.js'),[id],{windowsHide:true,serialization:'advanced',stdio:['ignore','pipe','pipe','ipc']});
    this.child.stdout.on('data',chunk=>process.stdout.write(`[${id}] ${chunk}`));this.child.stderr.on('data',chunk=>process.stderr.write(`[${id}] ${chunk}`));
    this.child.on('message',message=>{
      if(message.type==='ready')this.resolveReady();
      if(message.state)this.state={...this.state,...message.state,config:configs[id]};
      if(message.type==='frame')this.jpeg=Buffer.from(message.jpeg);
      if(message.type==='reply'){const pending=this.pending.get(message.requestId);if(pending){clearTimeout(pending.timer);this.pending.delete(message.requestId);message.error?pending.reject(Error(message.error)):pending.resolve(this.state);}}
    });
    this.child.on('error',error=>this.failed(error));
    this.child.on('exit',()=>{this.exited=true;this.failed(Error('Display worker stopped'));});
  }
  failed(error){this.state.running=false;this.state.connected=false;this.state.actualFps=0;if(!shuttingDown)this.state.error=error.message;this.rejectReady(error);for(const pending of this.pending.values()){clearTimeout(pending.timer);pending.reject(error);}this.pending.clear();}
  async request(action,config){
    await this.ready;if(this.exited)throw Error('Display worker is unavailable');
    return new Promise((resolve,reject)=>{const requestId=++this.serial;const timer=setTimeout(()=>{this.pending.delete(requestId);reject(Error('Display action timed out'));},30000);this.pending.set(requestId,{resolve,reject,timer});this.child.send({action,config,requestId},error=>{if(error){clearTimeout(timer);this.pending.delete(requestId);reject(error);}});});
  }
}
function client(id){let value=clients.get(id);if(!value||value.exited){value=new DisplayClient(id);clients.set(id,value);}return value;}
function status(){return {name:'Miku Screen Hub',version:'1.2.0',shutdownWatcher:sessionWatcher.state,screens:Object.fromEntries(IDS.map(id=>[id,clients.get(id)?.state||{...descriptors[id],id,running:false,connected:false,config:configs[id]}])),telemetry:telemetry.snapshot()};}
async function powerOffDisplays(reason){
  if(poweringOff)return poweringOff;
  poweringOff=(async()=>{
    const active=IDS.filter(id=>{const state=clients.get(id)?.state;return state&&state.device&&(state.running||state.connected||!['black','suspended'].includes(state.powerState));});
    const results=await Promise.allSettled(active.map(id=>clients.get(id).request('power-off',configs[id])));
    const summary=results.map((result,index)=>({id:active[index],ok:result.status==='fulfilled',error:result.status==='rejected'?result.reason.message:null}));
    fs.appendFileSync(path.join(DATA,'shutdown.log'),new Date().toISOString()+' '+JSON.stringify({reason,results:summary})+'\n');
    return {ok:summary.every(result=>result.ok),results:summary};
  })();try{return await poweringOff;}finally{poweringOff=null;}
}
function save(){const file=path.join(DATA,'config.json');fs.writeFileSync(file+'.tmp',JSON.stringify(configs,null,2));fs.renameSync(file+'.tmp',file);}
function json(res,code,value){res.writeHead(code,{'Content-Type':'application/json; charset=utf-8','Cache-Control':'no-store'});res.end(JSON.stringify(value));}
const routes=new Map([
  ['/', ['panel.html','text/html; charset=utf-8']],
  ['/screens/aigo',['aigo.html','text/html; charset=utf-8']],
  ['/screens/asus',['asus.html','text/html; charset=utf-8']],
  ['/assets/rurudo-miku-transform.webp',['assets/rurudo-miku-transform.webp','image/webp']],
  ['/assets/rurudo-miku-cutout.png',['assets/rurudo-miku-cutout.png','image/png']],
  ['/assets/rurudo-miku-game.webp',['assets/rurudo-miku-game.webp','image/webp']]
]);
const server=http.createServer(async(req,res)=>{
  if(shuttingDown){json(res,503,{error:'Program is exiting'});return;}
  if(req.headers.host!==`${HOST}:${PORT}`||(req.headers.origin&&req.headers.origin!==BASE)){json(res,403,{error:'Local requests only'});return;}
  try{
    const url=new URL(req.url,BASE);
    if(req.method==='GET'&&url.pathname==='/api/status'){json(res,200,status());return;}
    if(req.method==='GET'&&url.pathname==='/api/stats'){json(res,200,telemetry.snapshot());return;}
    const preview=/^\/preview\/(aigo|asus)\.jpg$/.exec(url.pathname);
    if(req.method==='GET'&&preview){const jpeg=clients.get(preview[1])?.jpeg;if(!jpeg){json(res,404,{error:'Waiting for first frame'});return;}res.writeHead(200,{'Content-Type':'image/jpeg','Cache-Control':'no-store'});res.end(jpeg);return;}
    if(req.method==='GET'&&routes.has(url.pathname)){
      const [file,type]=routes.get(url.pathname);res.writeHead(200,{'Content-Type':type,'Cache-Control':file.startsWith('assets/')?'public, max-age=86400':'no-store'});
      const stream=fs.createReadStream(path.join(__dirname,'public',file));stream.on('error',()=>res.destroy());stream.pipe(res);return;
    }
    if(req.method!=='POST'){json(res,404,{error:'Not found'});return;}
    let body='';for await(const chunk of req){body+=chunk;if(body.length>8192)throw Error('Request too large');}const input=body?JSON.parse(body):{};
    if(url.pathname==='/api/session-ending'){
      if(input.token!==sessionToken){json(res,403,{error:'Invalid session token'});return;}
      sessionEnding=true;const result=await powerOffDisplays(input.reason||'session-ending');json(res,200,result);setTimeout(shutdown,100);return;
    }
    if(sessionEnding){json(res,503,{error:'Windows session is ending'});return;}
    if(url.pathname==='/api/power-off-all'){json(res,200,await powerOffDisplays('manual'));return;}
    if(url.pathname==='/api/exit'){json(res,200,{exiting:true});setTimeout(shutdown,100);return;}
    if(['/api/start-all','/api/stop-all'].includes(url.pathname)){
      const action=url.pathname==='/api/start-all'?'start':'stop';
      const results=await Promise.allSettled(IDS.map(id=>client(id).request(action,configs[id])));
      json(res,200,{...status(),results:results.map((result,index)=>({id:IDS[index],ok:result.status==='fulfilled',error:result.status==='rejected'?result.reason.message:null}))});return;
    }
    const match=/^\/api\/screens\/(aigo|asus)\/(start|stop|config)$/.exec(url.pathname);
    if(!match){json(res,404,{error:'Unknown action'});return;}
    const [,id,action]=match;
    if(action==='config'){configs[id]=validateConfig(id,input,configs[id]);save();await client(id).request('configure',configs[id]);}
    else await client(id).request(action,configs[id]);
    json(res,200,status());
  }catch(error){json(res,400,{error:error.message});}
});
function openPanel(){if(process.platform==='win32'){const child=spawn('rundll32.exe',['url.dll,FileProtocolHandler',BASE],{windowsHide:true,detached:true,stdio:'ignore'});child.on('error',()=>{});child.unref();}}
server.on('error',error=>{console.error(error.code==='EADDRINUSE'?`程序可能已在运行，请打开 ${BASE}`:error.message);if(error.code==='EADDRINUSE'&&process.argv.includes('--open'))openPanel();process.exitCode=1;});
server.listen(PORT,HOST,()=>{
  console.log(`Miku Screen Hub: ${BASE}`);telemetry.start();for(const id of IDS)client(id);
  sessionWatcher.start(BASE,sessionToken);
  if(process.argv.includes('--open'))openPanel();
  if(process.argv.includes('--start'))Promise.allSettled(IDS.map(id=>client(id).request('start',configs[id]))).then(results=>{for(let i=0;i<results.length;i++)if(results[i].status==='rejected')console.error(IDS[i]+': '+results[i].reason.message);});
});
async function shutdown(){
  if(shuttingDown)return;shuttingDown=true;telemetry.close();server.close();
  const force=setTimeout(()=>{for(const value of clients.values())value.child.kill();process.exit(0);},12000);
  await powerOffDisplays('program-exit');sessionWatcher.close();
  await Promise.allSettled([...clients.values()].map(value=>value.request('shutdown')));clearTimeout(force);
  for(const value of clients.values())value.child.disconnect();setTimeout(()=>process.exit(0),200);
}
process.on('SIGINT',shutdown);process.on('SIGTERM',shutdown);
