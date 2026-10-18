'use strict';
const fs=require('node:fs'),path=require('node:path');
const {spawn}=require('node:child_process');
const {Renderer,delay}=require('./renderer');
const id=process.argv[2];if(!['aigo','asus'].includes(id))throw Error('Unknown display');
const protocol=require('./protocols/'+id),hid=require('./vendor/hid.node');
const state={id,connected:false,running:false,sent:0,bytes:0,actualFps:0,retries:0,reconnects:0,reconnecting:false,retryInMs:0,error:'',device:null,config:null,workerVersion:'1.2.0'};
let raw,initRaw,renderer,timer,inFlight,retryTimer,recoveryAttempts=0,wantRunning=false,generation=0,sequence=0,config,queue=Promise.resolve(),closing=false;
const notify=message=>{if(process.connected)process.send(message,()=>{});};
const publish=()=>notify({type:'state',state});
function disconnect(){if(raw){try{raw.close();}catch{}raw=null;}if(initRaw){try{initRaw.close();}catch{}initRaw=null;}state.connected=false;if(id==='asus')state.initialized=false;}
async function closeRenderer(){const previous=renderer;renderer=null;if(previous)await previous.close();}
function cancelRecovery(){clearTimeout(retryTimer);retryTimer=null;state.reconnecting=false;state.retryInMs=0;}
function scheduleRecovery(){
  if(!wantRunning||closing||retryTimer)return;
  const wait=Math.min(30000,1000*2**Math.min(recoveryAttempts++,5));state.reconnecting=true;state.retryInMs=wait;
  retryTimer=setTimeout(()=>{retryTimer=null;queue=queue.then(async()=>{if(wantRunning&&!state.running&&!closing){state.reconnects++;try{await start();}catch{}}}).catch(error=>console.error(error.stack));},wait);
}
async function fail(error){
  state.running=false;state.actualFps=0;generation++;clearTimeout(timer);disconnect();await closeRenderer();
  state.error=error.message;state.lastError=error.message;state.lastErrorAt=new Date().toISOString();
  console.error(`[${state.lastErrorAt}] ${error.stack||error.message}`);scheduleRecovery();publish();
}
function command(name,body){const seq=sequence++;raw.write([...protocol.command(name,seq,body)]);const reply=protocol.response(raw.readTimeout(2500));if(Number(reply.headers.AckNumber)!==seq+1)throw Error('Unexpected response sequence');return reply.body?JSON.parse(reply.body):null;}
async function connect(){
  if(raw)return;
  if(id==='aigo'){
    const devices=hid.devices(0x1d6b,0x0103).filter(device=>device.product==='Aigo USB Device'&&device.usagePage===12);
    if(devices.length!==1)throw Error('Expected one Aigo screen; found '+devices.length);
    raw=new hid.HID(devices[0].path);sequence=0;
    const info=command('conn');if(!info||info.OS!=='Linux')throw Error('Unrecognized Aigo handshake');
    command('power',{event:'resume'});command('realtimeDisplay',{enable:true});
    state.device={name:'爱国者长条屏',width:protocol.WIDTH,height:protocol.HEIGHT,version:info.version};
  }else{
    const devices=hid.devices(0x0b05,0x1ca9).filter(device=>device.interface===1&&device.usagePage===65286);
    if(devices.length!==1)throw Error('Expected one ASUS LCD interface; found '+devices.length);
    const initDevices=hid.devices(0x0b05,0x1ca9).filter(device=>device.interface===0&&device.usagePage===65286&&device.serialNumber===devices[0].serialNumber);
    if(initDevices.length!==1)throw Error('Expected matching ASUS LCD initialization interface');
    initRaw=new hid.HID(initDevices[0].path);raw=new hid.HID(devices[0].path);
    if(initRaw.write([...protocol.initializationReport()])!==441)throw Error('Incomplete LCD initialization write');
    await delay(1000);state.initialized=true;
    state.device={name:'华硕水冷屏',width:protocol.WIDTH,height:protocol.HEIGHT,serial:devices[0].serialNumber};
  }
  state.connected=true;
}
function compatibleJpeg(input){return new Promise((resolve,reject)=>{
  const filter={90:'transpose=1',180:'hflip,vflip',270:'transpose=2'}[config.rotation];
  const child=spawn(process.env.ASUS_FFMPEG||'ffmpeg',['-hide_banner','-loglevel','error','-f','image2pipe','-i','pipe:0','-frames:v','1',...(filter?['-vf',filter]:[]),'-c:v','mjpeg','-pix_fmt','yuvj420p','-huffman','default','-q:v','4','-f','image2pipe','pipe:1'],{windowsHide:true,stdio:['pipe','pipe','pipe']});
  const chunks=[],errors=[];let size=0;const timeout=setTimeout(()=>{child.kill();reject(Error('JPEG conversion timed out'));},6000);
  child.stdout.on('data',chunk=>{size+=chunk.length;if(size>2*1024*1024){child.kill();reject(Error('JPEG output too large'));}else chunks.push(chunk);});
  child.stderr.on('data',chunk=>errors.push(chunk));child.on('error',error=>{clearTimeout(timeout);reject(error);});
  child.on('close',code=>{clearTimeout(timeout);code===0?resolve(Buffer.concat(chunks)):reject(Error('JPEG conversion failed: '+Buffer.concat(errors).toString().slice(0,200)));});
  child.stdin.on('error',()=>{});child.stdin.end(input);
});}
async function upload(jpeg,token,attempts=3,ackTimeout=1500){
  if(id==='aigo'){for(const packet of protocol.imagePackets(jpeg))raw.write([...packet]);return;}
  const reports=[...protocol.imagePackets(jpeg)].map(protocol.windowsReport);
  for(let attempt=0;attempt<attempts;attempt++){
    if(token!==undefined&&token!==generation)return;
    try{
      for(let stale=0;stale<64;stale++){if(raw.readTimeout(0).length===0)break;}
      for(const report of reports){if(raw.write([...report])!==1025)throw Error('Incomplete HID write');}
      protocol.validateAck(raw.readTimeout(ackTimeout));return;
    }catch(error){if(attempt===attempts-1)throw error;state.retries++;await delay(100);}
  }
}
async function tick(token){
  const began=Date.now();
  try{
    let jpeg=await renderer.capture(config.quality);
    if(token!==generation||!state.running)return;
    if(id==='asus')jpeg=await compatibleJpeg(jpeg);
    if(token!==generation||!state.running)return;
    await upload(jpeg,token);
    if(token!==generation||!state.running)return;
    if(state.sent%300===0)fs.writeFileSync(path.join(__dirname,'data',id,'last-frame.jpg'),jpeg);
    state.sent++;state.bytes+=jpeg.length;state.error='';state.powerState='on';recoveryAttempts=0;state.actualFps=Math.round(1000/Math.max(1000/config.fps,Date.now()-began)*10)/10;
    notify({type:'frame',jpeg,state});
  }catch(error){if(token===generation)await fail(error);}
  finally{if(state.running&&token===generation)timer=setTimeout(()=>runTick(token),Math.max(1,1000/config.fps-(Date.now()-began)));}
}
function runTick(token){inFlight=tick(token).finally(()=>{inFlight=null;});}
async function stop(){wantRunning=false;cancelRecovery();state.running=false;state.actualFps=0;generation++;clearTimeout(timer);if(inFlight)await inFlight;disconnect();await closeRenderer();publish();}
async function powerOff(){
  wantRunning=false;cancelRecovery();state.running=false;state.actualFps=0;generation++;clearTimeout(timer);
  // Closing CDP first cancels a screenshot that might otherwise hold up shutdown.
  await closeRenderer();if(inFlight)await inFlight;
  try{
    await connect();const jpeg=fs.readFileSync(path.join(__dirname,'public','assets','black-'+id+'.jpg'));
    await upload(jpeg,undefined,1,700);state.sent++;state.bytes+=jpeg.length;
    state.powerState='black';
    if(id==='aigo'){command('power',{event:'suspend'});state.powerState='suspended';}
    state.error='';notify({type:'frame',jpeg,state});
  }catch(error){state.error='Power off: '+error.message;throw error;}
  finally{disconnect();publish();}
}
async function start(){
  if(state.running)return;
  cancelRecovery();if(inFlight)await inFlight;await closeRenderer();disconnect();state.error='';
  try{
    await connect();renderer=new Renderer(id,protocol.WIDTH,protocol.HEIGHT);await renderer.open(config.url);
    // Replay the last acknowledged frame to resume a previously initialized ASUS LCD session.
    if(id==='asus'){
      const file=path.join(__dirname,'data',id,'last-frame.jpg');
      if(fs.existsSync(file)){try{await upload(fs.readFileSync(file));}catch{}}
    }
    state.running=true;const token=++generation;publish();runTick(token);
  }catch(error){await fail(error);throw error;}
}
async function action(message){
  if(message.action==='configure'){
    const resume=wantRunning;await stop();config=message.config;state.config=config;state.error='';if(resume){wantRunning=true;await start();}else publish();
  }else if(message.action==='start'){wantRunning=true;if(message.config){config=message.config;state.config=config;}await start();}
  else if(message.action==='stop')await stop();
  else if(message.action==='power-off'){if(message.config){config=message.config;state.config=config;}await powerOff();}
  else if(message.action==='shutdown'){closing=true;await stop();}
  else throw Error('Unknown worker action');
  notify({type:'reply',requestId:message.requestId,state});
  if(closing)setTimeout(()=>process.exit(0),100);
}
process.on('message',message=>{queue=queue.then(()=>action(message)).catch(error=>{state.error=error.message;publish();notify({type:'reply',requestId:message.requestId,error:error.message,state});});});
process.on('disconnect',()=>{stop().finally(()=>process.exit(0));});
process.on('SIGTERM',()=>{stop().finally(()=>process.exit(0));});
notify({type:'ready'});
