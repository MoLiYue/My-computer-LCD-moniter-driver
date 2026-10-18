'use strict';
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path');
const {spawn}=require('node:child_process');
module.exports=async function(){
 if(process.platform!=='win32')return;
 const secret='a'.repeat(64),requests=[];let child,output='';
 const server=http.createServer(async(req,res)=>{let body='';for await(const chunk of req)body+=chunk;requests.push({method:req.method,url:req.url,body:JSON.parse(body)});res.writeHead(200,{'Content-Type':'application/json'});res.end('{"ok":true,"results":[]}');});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 try{
  child=spawn(path.join(__dirname,'ShutdownWatcher.exe'),[`http://127.0.0.1:${server.address().port}`,String(process.pid),secret,'--self-test'],{windowsHide:true,stdio:['ignore','pipe','pipe']});
  child.stdout.on('data',chunk=>output+=chunk);child.stderr.on('data',chunk=>output+=chunk);
  const code=await new Promise((resolve,reject)=>{const timer=setTimeout(()=>{child.kill();reject(Error('Native watcher test timed out: '+output));},7000);child.once('error',error=>{clearTimeout(timer);reject(error);});child.once('exit',code=>{clearTimeout(timer);resolve(code);});});
  assert.equal(code,0,output);assert.equal(requests.length,1,'Only confirmed end-session should send a request');assert.equal(requests[0].method,'POST');assert.equal(requests[0].url,'/api/session-ending');assert.equal(requests[0].body.token,secret);assert.equal(requests[0].body.reason,'shutdown-or-restart');
  assert.ok(output.includes('CANCELLED: no display action'));assert.ok(output.includes('SELF_TEST_COMPLETE'));
  console.log('Native shutdown watcher passed: query allowed, cancellation ignored, confirmed event delivered once. No Windows shutdown was requested.');
 }finally{if(child&&child.exitCode===null)child.kill();await new Promise(resolve=>server.close(resolve));}
};
if(require.main===module)module.exports().catch(error=>{console.error(error);process.exitCode=1;});
