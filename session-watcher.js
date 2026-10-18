'use strict';
const fs=require('node:fs'),path=require('node:path');
const {spawn}=require('node:child_process');
class SessionWatcher {
  constructor(){this.state={enabled:false,ready:false,pid:null,error:null};}
  start(base,token){
    if(process.platform!=='win32'||process.env.SCREEN_HUB_NO_SESSION_WATCHER==='1')return;
    const exe=path.join(__dirname,'ShutdownWatcher.exe');if(!fs.existsSync(exe)){this.state.error='ShutdownWatcher.exe is missing';return;}
    this.child=spawn(exe,[base,String(process.pid),token],{windowsHide:true,stdio:['ignore','pipe','pipe']});
    this.state.enabled=true;this.state.pid=this.child.pid;let pending='';
    this.child.stdout.setEncoding('utf8');this.child.stdout.on('data',chunk=>{pending+=chunk;let end;while((end=pending.indexOf('\n'))>=0){const line=pending.slice(0,end).trim();pending=pending.slice(end+1);console.log('[session] '+line);if(line.endsWith(' READY'))this.state.ready=true;}});
    this.child.stderr.on('data',chunk=>console.error('[session] '+chunk.toString().slice(0,500)));
    this.child.on('error',error=>{this.state.error=error.message;this.state.ready=false;});
    this.child.on('exit',code=>{this.state.ready=false;if(!this.closing&&code)this.state.error='Shutdown watcher exited: '+code;});
  }
  close(){this.closing=true;if(this.child)this.child.kill();}
}
module.exports={SessionWatcher};
