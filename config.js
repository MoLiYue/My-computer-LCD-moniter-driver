'use strict';
const IDS=['aigo','asus'];
function validateConfig(id,input,current){
  if(!IDS.includes(id))throw Error('Unknown display');
  const url=new URL(input.url??current.url);if(!['http:','https:'].includes(url.protocol)||url.username||url.password)throw Error('Use an http/https URL without embedded credentials');
  const fps=Number(input.fps??current.fps),quality=Number(input.quality??current.quality),rotation=Number(input.rotation??current.rotation??0);
  if(!Number.isFinite(fps)||fps<1||fps>15||!Number.isInteger(quality)||quality<30||quality>95)throw Error('Invalid frame rate or quality');
  if(![0,90,180,270].includes(rotation)||(id==='aigo'&&rotation!==0))throw Error('Invalid rotation');
  return {url:url.href,fps,quality,rotation};
}
module.exports={IDS,validateConfig};
