'use strict';
// Wire-format facts: https://github.com/BeiyanYunyi/lcdd/blob/main/docs/protocol.md
// LCD session initialization on interface 0 and image upload on interface 1.
const WIDTH=320, HEIGHT=320;
function initializationReport(){const report=Buffer.alloc(441);Buffer.from([0x12,0x01,0x00,0x80,0x64]).copy(report,1);return report;}
function* imagePackets(jpeg) {
  if(jpeg[0]!==255||jpeg[1]!==216||jpeg[jpeg.length-2]!==255||jpeg[jpeg.length-1]!==217)throw Error('Invalid JPEG');
  const count=Math.ceil(jpeg.length/1020);
  if(count<1||count>39)throw Error('Image exceeds supported LCD transport size (39780 bytes)');
  for(let i=0;i<count;i++){
    const packet=Buffer.alloc(1024);
    packet[0]=8;
    packet.writeUInt16LE(i===0?count:i,1);
    packet[3]=i===0?128:0;
    jpeg.copy(packet,4,i*1020,(i+1)*1020);
    yield packet;
  }
}
function validateAck(data){
  const b=Buffer.from(data);
  if(b.length!==16||b[0]!==8||b[1]!==129||b.subarray(2).some(n=>n!==0))throw Error('LCD did not acknowledge the frame: '+b.toString('hex'));
  return true;
}
function windowsReport(packet){if(packet.length!==1024)throw Error('Invalid HID payload length');return Buffer.concat([Buffer.from([0]),packet]);}
module.exports={initializationReport,imagePackets,windowsReport,validateAck,WIDTH,HEIGHT};
