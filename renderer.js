'use strict';
const fs=require('node:fs'),path=require('node:path');
const {spawn,execFile}=require('node:child_process');
const {promisify}=require('node:util');
const {WebViewRenderer}=require('./webview-renderer');
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const hasExited=child=>child.exitCode!==null||child.signalCode!==null;
const exec=promisify(execFile);
async function cleanProfile(profile){
  if(process.platform!=='win32')return;
  const powershell=path.join(process.env.SystemRoot||'C:/Windows','System32/WindowsPowerShell/v1.0/powershell.exe');
  try{
    const {stdout}=await exec(powershell,['-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File',path.join(__dirname,'renderer-cleanup.ps1'),'-Profile',profile],{windowsHide:true,timeout:7000,maxBuffer:32*1024});
    if(stdout.trim())console.log(stdout.trim());
  }catch(error){throw Error('Renderer profile check failed: '+(error.stderr||error.message).trim().split(/\r?\n/)[0]);}
}
function waitForExit(child,ms){
  if(hasExited(child)||!child.pid)return Promise.resolve(true);
  return new Promise(resolve=>{
    const exited=()=>{clearTimeout(timer);resolve(true);};
    const timer=setTimeout(()=>{child.off('exit',exited);resolve(false);},ms);
    child.once('exit',exited);
  });
}
class CDP {
  constructor(ws){this.ws=ws;this.serial=0;this.requests=new Map();ws.onmessage=({data})=>{const message=JSON.parse(data),pending=this.requests.get(message.id);if(pending){clearTimeout(pending.timer);this.requests.delete(message.id);message.error?pending.reject(Error(message.error.message)):pending.resolve(message.result);}};ws.onclose=()=>{for(const pending of this.requests.values()){clearTimeout(pending.timer);pending.reject(Error('Renderer disconnected'));}this.requests.clear();};}
  call(method,params={}){return new Promise((resolve,reject)=>{const id=++this.serial;const timer=setTimeout(()=>{this.requests.delete(id);reject(Error('Renderer timeout: '+method));},10000);this.requests.set(id,{resolve,reject,timer});this.ws.send(JSON.stringify({id,method,params}));});}
  close(closeBrowser=false){
    if(closeBrowser&&this.ws.readyState===WebSocket.OPEN){
      try{this.ws.send(JSON.stringify({id:++this.serial,method:'Browser.close'}));}catch{}
    }
    this.ws.close();
  }
}
class Renderer {
  constructor(id,width,height){this.id=id;this.width=width;this.height=height;}
  async open(url){
    const exe=[process.env.SCREEN_BROWSER,process.env.AIGO_BROWSER,'C:/Program Files/Google/Chrome/Application/chrome.exe','C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'].find(file=>file&&fs.existsSync(file));
    if(!exe)throw Error('Chrome or Edge is required');
    const profile=path.join(__dirname,'data',this.id,'renderer');fs.mkdirSync(profile,{recursive:true});
    await cleanProfile(profile);
    const portFile=path.join(profile,'DevToolsActivePort');try{fs.unlinkSync(portFile);}catch{}
    const browser=this.browser=spawn(exe,['--headless=new','--remote-debugging-port=0','--remote-debugging-address=127.0.0.1',`--user-data-dir=${profile}`,'--no-first-run','--no-default-browser-check','--disable-extensions','--disable-background-timer-throttling',`--window-size=${this.width},${this.height}`,'about:blank'],{windowsHide:true,stdio:['ignore','ignore','pipe']});
    let launchError,port,stderr='';browser.on('error',e=>launchError=e);
    browser.stderr.setEncoding('utf8').on('data',chunk=>{stderr=(stderr+chunk).slice(-2048);});
    browser.once('exit',()=>{if(this.browser===browser)this.cdp?.close();});
    const exitError=()=>Error(`Renderer exited (code ${browser.exitCode??browser.signalCode})${stderr.trim()?': '+stderr.trim().split(/\r?\n/).at(-1):''}`);
    for(let i=0;i<100;i++){if(launchError)throw launchError;if(hasExited(browser))throw exitError();try{port=Number(fs.readFileSync(portFile,'utf8').split('\n')[0]);if(port)break;}catch{}await delay(100);}
    if(!port)throw Error('Renderer did not start');
    let target;
    for(let attempt=0;attempt<50;attempt++){
      try{const targets=await(await fetch(`http://127.0.0.1:${port}/json/list`,{signal:AbortSignal.timeout(1000)})).json();target=targets.find(item=>item.type==='page');if(target)break;}catch{}
      if(hasExited(browser))throw exitError();await delay(100);
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
    const browser=this.browser;this.browser=null;
    const cdp=this.cdp;this.cdp=null;
    cdp?.close(true);
    if(browser&&!(await waitForExit(browser,cdp?650:0))){
      browser.kill();await waitForExit(browser,1200);
    }
  }
}
module.exports={Renderer:process.env.SCREEN_RENDERER==='chrome'?Renderer:WebViewRenderer,ChromeRenderer:Renderer,delay};
