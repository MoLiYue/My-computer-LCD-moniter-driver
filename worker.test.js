'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
module.exports=async function(){
 const handles=[],browsers=[],captures=[],timers=[];
 class FakeHID{constructor(device){this.device=device;this.closed=0;handles.push(this);}write(data){assert.equal(data.length,this.device==='init'?441:1025);return data.length;}readTimeout(){return Buffer.from([8,129,...Array(14).fill(0)]);}close(){this.closed++;}}
 class FakeRenderer{constructor(){this.closed=0;browsers.push(this);}async open(){}capture(){return new Promise((resolve,reject)=>{this.pending={resolve,reject};captures.push(this.pending);});}async close(){this.closed++;if(this.pending)this.pending.reject(Error('Renderer closed'));}}
 const protocol=require('./protocols/asus');
 const context={Buffer,console:{error(){}},process:{argv:['node','display-worker.js','asus'],connected:true,send(message,callback){if(callback)callback();},on(){},exit(){}},setTimeout(callback,ms){const timer={callback,ms,cancelled:false};timers.push(timer);return timer;},clearTimeout(timer){if(timer)timer.cancelled=true;},__dirname,__filename:path.join(__dirname,'display-worker.js'),require(name){
  if(name==='node:fs')return {existsSync:()=>false,readFileSync:file=>fs.readFileSync(file)};
  if(name==='node:path')return path;
  if(name==='node:child_process')return {spawn(){throw Error('No FFmpeg expected in lifecycle test');}};
  if(name==='./renderer')return {Renderer:FakeRenderer,delay:async()=>{}};
  if(name==='./vendor/hid.node')return {HID:FakeHID,devices:()=>[{path:'init',interface:0,usagePage:65286,serialNumber:'test'},{path:'bulk',interface:1,usagePage:65286,serialNumber:'test'}]};
  if(name==='./protocols/asus')return protocol;
  throw Error('Unexpected dependency '+name);
 }};
 vm.createContext(context);vm.runInContext(fs.readFileSync(path.join(__dirname,'display-worker.js'),'utf8'),context);
 await vm.runInContext('action({action:"start",requestId:1,config:{url:"http://test/",fps:5,quality:80,rotation:180}})',context);
 assert.equal(handles.length,2);assert.ok(handles.every(handle=>handle.closed===0),'Initialization interface must remain open while displaying');
 const firstFrame=vm.runInContext('inFlight',context);captures[0].reject(Error('Renderer disconnected'));await firstFrame;
 assert.ok(handles.every(handle=>handle.closed===1),'Failure must close both HID interfaces');assert.equal(browsers[0].closed,1,'Failure must close the browser before retry');
 const retry=timers.find(timer=>timer.ms===1000&&!timer.cancelled);assert.ok(retry,'Failure must schedule recovery');retry.callback();await vm.runInContext('queue',context);
 assert.equal(browsers.length,2);assert.equal(vm.runInContext('state.reconnects',context),1);assert.equal(handles.length,4);assert.ok(handles.slice(2).every(handle=>handle.closed===0));
 const stopping=vm.runInContext('action({action:"stop",requestId:2})',context);captures[1].reject(Error('Cancelled during manual stop'));await stopping;
 assert.ok(handles.every(handle=>handle.closed===1));assert.ok(browsers.every(browser=>browser.closed===1));assert.equal(vm.runInContext('wantRunning',context),false);
 assert.ok(timers.every(timer=>timer.cancelled||timer===retry),'Manual stop must not leave an active recovery timer');
 const before=browsers.length;retry.callback();await vm.runInContext('queue',context);assert.equal(browsers.length,before,'Stale queued recovery must respect manual stop');
 await vm.runInContext('action({action:"start",requestId:3})',context);
 await vm.runInContext('action({action:"power-off",requestId:4})',context);
 assert.equal(vm.runInContext('state.powerState',context),'black');assert.equal(vm.runInContext('state.running',context),false);assert.ok(handles.every(handle=>handle.closed===1));
 console.log('Worker lifecycle passed: held init handle, failure cleanup, recovery, stop cancellation, black-frame power off.');
};
if(require.main===module)module.exports().catch(error=>{console.error(error);process.exitCode=1;});
