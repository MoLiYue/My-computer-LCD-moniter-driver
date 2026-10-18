'use strict';
const WIDTH = 1920, HEIGHT = 462;

function command(name, seq, body, now = Date.now()) {
  if (!['conn', 'power', 'realtimeDisplay'].includes(name)) throw Error('Unsupported command');
  let text = `POST ${name} 1\r\nSeqNumber=${seq}\r\nDate=${now}\r\n`;
  let content = Buffer.alloc(0);
  if (body !== undefined) {
    content = Buffer.from(JSON.stringify(body));
    text += `ContentType=json\r\nContentLength=${content.length}\r\n`;
  }
  const payload = Buffer.concat([Buffer.from(text + '\r\n'), content]);
  const size = payload.length + 5;
  const middle = Buffer.alloc(payload.length + 3);
  middle.writeUInt16BE(size, 0); payload.copy(middle, 2);
  middle[middle.length - 1] = middle.subarray(0, -1).reduce((a, b) => (a + b) & 255, 0);
  const escaped = [];
  for (const b of middle) {
    if (b === 0x5a) escaped.push(0x5b, 1);
    else if (b === 0x5b) escaped.push(0x5b, 2);
    else escaped.push(b);
  }
  return Buffer.from([0, 0x5a, ...escaped, 0x5a]);
}

function response(input) {
  const data = Buffer.from(input);
  const start = data.indexOf(0x5a);
  if (start < 0) throw Error('Missing response frame');
  const bytes = [];
  let ended = false;
  for (let i = start + 1; i < data.length; i++) {
    if (data[i] === 0x5a) { ended = true; break; }
    if (data[i] === 0x5b) {
      const code = data[++i];
      if (code !== 1 && code !== 2) throw Error('Invalid response escape');
      bytes.push(code === 1 ? 0x5a : 0x5b);
    } else bytes.push(data[i]);
  }
  const b = Buffer.from(bytes);
  if (!ended || b.length < 4 || b.readUInt16BE(0) !== b.length + 2) throw Error('Incomplete response');
  if (b.subarray(0, -1).reduce((a, v) => (a + v) & 255, 0) !== b[b.length - 1]) throw Error('Response checksum mismatch');
  const msg = b.subarray(2, -1).toString('utf8');
  const split = msg.indexOf('\r\n\r\n');
  if (split < 0) throw Error('Missing response headers');
  const lines = msg.slice(0, split).split('\r\n');
  const code = Number(lines.shift().split(' ')[1]);
  const headers = Object.fromEntries(lines.map(s => { const i = s.indexOf('='); return [s.slice(0, i), s.slice(i + 1)]; }));
  if (code !== 200) throw Error(`Device returned ${code}`);
  return { code, headers, body: msg.slice(split + 4) };
}

function* imagePackets(jpeg) {
  if (jpeg[0] !== 255 || jpeg[1] !== 216 || jpeg[jpeg.length - 2] !== 255 || jpeg[jpeg.length - 1] !== 217) throw Error('Invalid JPEG');
  for (let offset = 0, block = 0; offset < jpeg.length; offset += 1000, block++) {
    const chunk = jpeg.subarray(offset, offset + 1000);
    const packet = Buffer.alloc(25 + chunk.length);
    packet[1] = 0x5c;
    packet.writeUInt16BE(packet.length - 4, 2);
    packet[4] = 8;
    packet.writeUInt16BE(Math.ceil(jpeg.length / 1000), 5);
    packet.writeUInt16BE(block, 7);
    packet[9] = 1;
    chunk.copy(packet, 25);
    yield packet;
  }
}
module.exports = { command, response, imagePackets, WIDTH, HEIGHT };
