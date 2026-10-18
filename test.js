'use strict';
const assert=require('node:assert/strict'),path=require('node:path');
const {spawn}=require('node:child_process');
const {validateConfig}=require('./config');
const {Telemetry}=require('./telemetry');
const {initializationReport}=require('./protocols/asus');
const init=initializationReport();assert.equal(init.length,441);assert.equal(init.subarray(0,9).toString('hex'),'001201008064000000');assert.ok(init.subarray(6).every(value=>value===0));
const defaults={url:'http://127.0.0.1:19330/screens/asus',fps:5,quality:80,rotation:180};
assert.deepEqual(validateConfig('asus',{fps:3},defaults),{...defaults,fps:3});
for(const input of [{url:'file:///C:/test'},{url:'javascript:alert(1)'},{fps:0},{quality:80.5},{rotation:45}])assert.throws(()=>validateConfig('asus',input,defaults));
assert.throws(()=>validateConfig('aigo',{rotation:180},defaults));
assert.throws(()=>validateConfig('unknown',{},defaults));
const telemetry=new Telemetry(),now=Date.now();
telemetry.gpu={updatedAt:now,gpu:0,vram:25,vramUsedBytes:4*2**30,vramTotalBytes:16*2**30,gpuName:'test'};
telemetry.temperature={updatedAt:now,cpuTemperature:56};
assert.equal(telemetry.snapshot(now).gpu,0);assert.equal(telemetry.snapshot(now).vramTotalBytes,16*2**30);assert.equal(telemetry.snapshot(now).cpuTemperature,56);
assert.equal(telemetry.snapshot(now+10001).gpu,null);assert.equal(telemetry.snapshot(now+10001).cpuTemperature,null);
telemetry.gpu.error='Reader failed';assert.equal(telemetry.snapshot(now).vram,null);
const base='http://127.0.0.1:19331';
const child=spawn(process.execPath,[path.join(__dirname,'app.js')],{env:{...process.env,SCREEN_HUB_PORT:'19331',SCREEN_HUB_NO_SESSION_WATCHER:'1'},windowsHide:true,stdio:['ignore','pipe','pipe']});
let output='';child.stdout.on('data',chunk=>output+=chunk);child.stderr.on('data',chunk=>output+=chunk);
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
(async()=>{
 try{
  await require('./worker.test')();
  await require('./shutdown-watcher.test')();
  let ready=false;for(let i=0;i<60;i++){try{if((await fetch(base+'/api/status')).ok){ready=true;break;}}catch{}await delay(100);}assert.ok(ready,'Control server did not start: '+output);
  const initial=await(await fetch(base+'/api/status')).json();assert.equal(initial.screens.aigo.running,false);assert.equal(initial.screens.asus.running,false);
  for(const url of ['/','/screens/aigo','/screens/asus','/assets/rurudo-miku-transform.webp','/assets/rurudo-miku-cutout.png'])assert.equal((await fetch(base+url)).status,200,url);
  assert.equal((await fetch(base+'/api/status',{headers:{Origin:'https://example.com'}})).status,403);
  assert.equal((await fetch(base+'/api/screens/other/start',{method:'POST',body:'{}'})).status,404);
  assert.equal((await fetch(base+'/api/screens/asus/config',{method:'POST',body:JSON.stringify({fps:0})})).status,400);
  const after=await(await fetch(base+'/api/status')).json();assert.deepEqual(after.screens.asus.config,initial.screens.asus.config);
  assert.equal((await fetch(base+'/vendor/hid.node')).status,404);
  assert.equal((await fetch(base+'/api/session-ending',{method:'POST',body:JSON.stringify({token:'invalid'})})).status,403);
  console.log('Passed: per-screen settings, GPU/temperature expiry and zero, both screen/assets routes, origin protection, invalid configuration rollback.');
 }finally{
  try{await fetch(base+'/api/exit',{method:'POST',body:'{}'});}catch{child.kill();}
  await new Promise(resolve=>{if(child.exitCode!==null){resolve();return;}const timeout=setTimeout(()=>{child.kill();resolve();},14000);child.once('exit',()=>{clearTimeout(timeout);resolve();});});
 }
})().catch(error=>{console.error(error);process.exitCode=1;});
