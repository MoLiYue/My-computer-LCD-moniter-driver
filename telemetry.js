'use strict';
const os=require('node:os'),path=require('node:path');
const {spawn}=require('node:child_process');
const numeric=value=>typeof value==='number'&&Number.isFinite(value)&&value>=0?value:null;
class Telemetry {
  constructor(){this.readers=[];this.gpu={updatedAt:0};this.temperature={updatedAt:0};this.previous=this.sampleCpu();this.cpu=null;}
  sampleCpu(){return os.cpus().reduce((total,cpu)=>{total.idle+=cpu.times.idle;total.total+=Object.values(cpu.times).reduce((a,b)=>a+b,0);return total;},{idle:0,total:0});}
  start(){
    this.timer=setInterval(()=>{const sample=this.sampleCpu(),elapsed=sample.total-this.previous.total;this.cpu=elapsed?Math.max(0,Math.min(100,100*(1-(sample.idle-this.previous.idle)/elapsed))):0;this.previous=sample;},1000);
    this.reader('gpu-reader.ps1','gpu');this.reader('temperature-reader.ps1','temperature');
  }
  reader(file,key){
    const exe=path.join(process.env.SystemRoot||'C:/Windows','System32/WindowsPowerShell/v1.0/powershell.exe');
    const child=spawn(exe,['-NoLogo','-NoProfile','-NonInteractive','-File',path.join(__dirname,file)],{windowsHide:true,stdio:['ignore','pipe','pipe']});this.readers.push(child);
    let pending='',diagnostic='';child.stdout.setEncoding('utf8');child.stdout.on('data',chunk=>{pending+=chunk;let end;while((end=pending.indexOf('\n'))>=0){const line=pending.slice(0,end).trim();pending=pending.slice(end+1);try{this[key]={...JSON.parse(line),updatedAt:Date.now()};}catch{}}});
    child.stderr.on('data',chunk=>{diagnostic=(diagnostic+chunk.toString()).slice(-500);});
    child.on('error',error=>{this[key]={updatedAt:0,error:error.message};});
    child.on('exit',()=>{this[key]={...this[key],updatedAt:0,error:diagnostic||'Sensor reader stopped'};});
  }
  snapshot(now=Date.now()){
    const gpuFresh=now-this.gpu.updatedAt<10000&&!this.gpu.error,tempFresh=now-this.temperature.updatedAt<10000&&!this.temperature.error;
    const memoryTotalBytes=os.totalmem(),memoryUsedBytes=memoryTotalBytes-os.freemem();
    return {cpu:this.cpu,cpuTemperature:tempFresh?numeric(this.temperature.cpuTemperature):null,temperatureSource:this.temperature.source||'ASUS ACPI',temperatureError:this.temperature.error||(!tempFresh?'Temperature data is stale':null),gpuName:this.gpu.gpuName||null,gpu:gpuFresh?numeric(this.gpu.gpu):null,vram:gpuFresh?numeric(this.gpu.vram):null,vramUsedBytes:gpuFresh?numeric(this.gpu.vramUsedBytes):null,vramTotalBytes:gpuFresh?numeric(this.gpu.vramTotalBytes):null,gpuSource:'Windows GPU counters / DXGI',gpuError:this.gpu.error||(!gpuFresh?'GPU data is stale':null),memory:100*memoryUsedBytes/memoryTotalBytes,memoryUsedBytes,memoryTotalBytes,uptime:os.uptime()};
  }
  close(){clearInterval(this.timer);for(const reader of this.readers)reader.kill();}
}
module.exports={Telemetry};
