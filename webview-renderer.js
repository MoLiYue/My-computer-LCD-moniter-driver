'use strict';
const fs=require('node:fs'),path=require('node:path'),readline=require('node:readline');
const {spawn}=require('node:child_process');

class WebViewRenderer {
  constructor(id,width,height){
    this.id=id;this.width=width;this.height=height;
  }
  async open(url){
    const exe=path.join(__dirname,'data','webview-bin','WebViewRenderer.exe');
    if(!fs.existsSync(exe))throw Error('WebView2 renderer is missing. Run build-webview.ps1 to build it.');
    const data=path.join(__dirname,'data','webview');
    const child=this.child=spawn(exe,[this.id,data,String(this.width),String(this.height)],{windowsHide:true,stdio:['pipe','pipe','pipe']});
    this.exited=new Promise(resolve=>{
      child.once('exit',(code,signal)=>{
        this.fail(Error(`WebView2 renderer exited (code ${code??signal})`));resolve();
      });
      child.once('error',error=>{this.fail(error);resolve();});
    });
    child.stdin.on('error',error=>this.fail(error));
    child.stderr.resume();
    this.lines=readline.createInterface({input:child.stdout,crlfDelay:Infinity});
    this.lines.on('line',line=>{
      if(line.startsWith('ERROR ')){this.fail(Error('WebView2: '+Buffer.from(line.slice(6),'base64').toString('utf8')));return;}
      const pending=this.pending;
      if(!pending)return;
      if(!line.startsWith(pending.prefix)){this.fail(Error('Unexpected WebView2 response'));return;}
      this.pending=null;clearTimeout(pending.timer);pending.resolve(line.slice(pending.prefix.length));
    });
    await this.request(new URL(url).href,'READY',18000);
  }
  fail(error){
    const pending=this.pending;if(!pending)return;
    this.pending=null;clearTimeout(pending.timer);pending.reject(error);
  }
  request(command,prefix,timeout){
    const child=this.child;
    if(!child||child.exitCode!==null||child.signalCode!==null||child.stdin.destroyed)return Promise.reject(Error('WebView2 renderer is not running'));
    if(this.pending)return Promise.reject(Error('WebView2 renderer already has a pending request'));
    return new Promise((resolve,reject)=>{
      const timer=setTimeout(()=>this.fail(Error('WebView2 timed out waiting for '+prefix.trim())),timeout);
      this.pending={resolve,reject,prefix,timer};
      child.stdin.write(command+'\n',error=>{if(error)this.fail(error);});
    });
  }
  async capture(quality){
    const body=await this.request('CAPTURE '+quality,'FRAME ',5000);
    const jpeg=Buffer.from(JSON.parse(body).data,'base64');
    if(jpeg[0]!==0xff||jpeg[1]!==0xd8)throw Error('WebView2 returned an invalid JPEG');
    return jpeg;
  }
  async close(){
    const child=this.child;this.child=null;
    if(!child)return;
    this.fail(Error('WebView2 renderer closed'));
    this.lines.close();
    if(child.exitCode!==null||child.signalCode!==null||!child.pid)return;
    if(!child.stdin.destroyed)child.stdin.end('QUIT\n');
    let timer;
    try{
      const stopped=await Promise.race([this.exited.then(()=>true),new Promise(resolve=>{timer=setTimeout(()=>resolve(false),650);})]);
      if(!stopped){
        child.kill();clearTimeout(timer);
        await Promise.race([this.exited,new Promise(resolve=>{timer=setTimeout(resolve,500);})]);
      }
    }finally{clearTimeout(timer);}
  }
}

module.exports={WebViewRenderer};
