'use strict';
const fs=require('node:fs'),path=require('node:path');
const {spawn}=require('node:child_process');
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
class CDP {
  constructor(ws){this.ws=ws;this.serial=0;this.requests=new Map();ws.onmessage=({data})=>{const message=JSON.parse(data),pending=this.requests.get(message.id);if(pending){clearTimeout(pending.timer);this.requests.delete(message.id);message.error?pending.reject(Error(message.error.message)):pending.resolve(message.result);}};ws.onclose=()=>{for(const pending of this.requests.values()){clearTimeout(pending.timer);pending.reject(Error('Renderer disconnected'));}this.requests.clear();};}
  call(method,params={}){return new Promise((resolve,reject)=>{const id=++this.serial;const timer=setTimeout(()=>{this.requests.delete(id);reject(Error('Renderer timeout: '+method));},10000);this.requests.set(id,{resolve,reject,timer});this.ws.send(JSON.stringify({id,method,params}));});}
  close(){this.ws.close();}
}
class Renderer {
  constructor(id,width,height){this.id=id;this.width=width;this.height=height;}
  async open(url){
    const exe=[process.env.SCREEN_BROWSER,process.env.AIGO_BROWSER,'C:/Program Files/Google/Chrome/Application/chrome.exe','C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'].find(file=>file&&fs.existsSync(file));
    if(!exe)throw Error('Chrome or Edge is required');
    const profile=path.join(__dirname,'data',this.id,'renderer');fs.mkdirSync(profile,{recursive:true});
    const portFile=path.join(profile,'DevToolsActivePort');try{fs.unlinkSync(portFile);}catch{}
    this.browser=spawn(exe,['--headless=new','--remote-debugging-port=0','--remote-debugging-address=127.0.0.1',`--user-data-dir=${profile}`,'--no-first-run','--no-default-browser-check','--disable-extensions','--disable-background-timer-throttling',`--window-size=${this.width},${this.height}`,'about:blank'],{windowsHide:true,stdio:'ignore'});
    let launchError,port;this.browser.on('error',e=>launchError=e);
    for(let i=0;i<100;i++){if(launchError)throw launchError;if(this.browser.exitCode!==null)throw Error('Renderer exited');try{port=Number(fs.readFileSync(portFile,'utf8').split('\n')[0]);if(port)break;}catch{}await delay(100);}
    if(!port)throw Error('Renderer did not start');
    let target;
    for(let attempt=0;attempt<50;attempt++){
      try{const targets=await(await fetch(`http://127.0.0.1:${port}/json/list`,{signal:AbortSignal.timeout(1000)})).json();target=targets.find(item=>item.type==='page');if(target)break;}catch{}
      if(this.browser.exitCode!==null)throw Error('Renderer exited');await delay(100);
    }
    if(!target)throw Error('Renderer debug endpoint is not ready');
    const ws=new WebSocket(target.webSocketDebuggerUrl);await new Promise((resolve,reject)=>{ws.onopen=resolve;ws.onerror=()=>reject(Error('Renderer connection failed'));});
    this.cdp=new CDP(ws);await this.cdp.call('Page.enable');
    await this.cdp.call('Emulation.setDeviceMetricsOverride',{width:this.width,height:this.height,deviceScaleFactor:1,mobile:false});
    const result=await this.cdp.call('Page.navigate',{url});if(result.errorText)throw Error(result.errorText);
    await this.cdp.call('Runtime.evaluate',{expression:'Promise.all([document.fonts.ready,...Array.from(document.images).map(img=>img.decode().catch(()=>{}))])',awaitPromise:true});
    await delay(700);
  }
  async capture(quality){const shot=await this.cdp.call('Page.captureScreenshot',{format:'jpeg',quality,fromSurface:true,captureBeyondViewport:false});return Buffer.from(shot.data,'base64');}
  async close(){
    if(this.cdp){this.cdp.close();this.cdp=null;}
    const browser=this.browser;this.browser=null;
    if(browser&&browser.exitCode===null){await new Promise(resolve=>{const timeout=setTimeout(resolve,2000);browser.once('exit',()=>{clearTimeout(timeout);resolve();});browser.kill();});}
  }
}
module.exports={Renderer,delay};
