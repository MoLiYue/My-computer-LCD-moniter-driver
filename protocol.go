package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const asusMaxJPEG = 39 * 1020

type lcdSession struct {
	id        string
	raw, init reportPort
	sequence  uint64
	device    map[string]any
}

func connectLCD(ctx context.Context, id string) (session *lcdSession, err error) {
	s := &lcdSession{id: id}
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	if id == "aigo" {
		devices, e := findHID(0x1d6b, 0x0103, 12)
		if e != nil {
			return nil, e
		}
		if len(devices) != 1 {
			return nil, fmt.Errorf("expected one Aigo LCD, found %d", len(devices))
		}
		s.raw, err = openHID(devices[0])
		if err != nil {
			return nil, err
		}
		var info struct {
			OS      string
			Version json.RawMessage
		}
		if err = s.command(ctx, "conn", nil, &info); err != nil {
			return nil, err
		}
		if info.OS != "Linux" {
			return nil, errors.New("unrecognized Aigo handshake")
		}
		if err = s.command(ctx, "power", map[string]string{"event": "resume"}, nil); err != nil {
			return nil, err
		}
		if err = s.command(ctx, "realtimeDisplay", map[string]bool{"enable": true}, nil); err != nil {
			return nil, err
		}
		s.device = map[string]any{"name": "爱国者长条屏", "width": 1920, "height": 462, "version": info.Version}
	} else {
		devices, e := findHID(0x0b05, 0x1ca9, 0xff06)
		if e != nil {
			return nil, e
		}
		var bulk, init []hidDevice
		for _, d := range devices {
			if d.iface == 1 {
				bulk = append(bulk, d)
			}
		}
		if len(bulk) != 1 {
			return nil, fmt.Errorf("expected one ASUS upload interface, found %d", len(bulk))
		}
		for _, d := range devices {
			if d.iface == 0 && d.serial == bulk[0].serial {
				init = append(init, d)
			}
		}
		if len(init) != 1 {
			return nil, errors.New("missing or ambiguous ASUS initialization interface")
		}
		s.init, err = openHID(init[0])
		if err != nil {
			return nil, err
		}
		s.raw, err = openHID(bulk[0])
		if err != nil {
			return nil, err
		}
		if err = s.initialize(ctx); err != nil {
			return nil, err
		}
		s.device = map[string]any{"name": "华硕水冷屏", "width": 320, "height": 320, "serial": bulk[0].serial}
	}
	return s, nil
}

func (s *lcdSession) close() {
	if s == nil {
		return
	}
	if s.raw != nil {
		s.raw.Close()
		s.raw = nil
	}
	if s.init != nil {
		s.init.Close()
		s.init = nil
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (s *lcdSession) initialize(ctx context.Context) error {
	report := make([]byte, 441)
	copy(report, []byte{0, 0x12, 0x01, 0, 0x80, 0x64})
	if err := s.init.Write(ctx, report); err != nil {
		return fmt.Errorf("ASUS initialization write: %w", err)
	}
	if err := sleepContext(ctx, time.Second); err != nil {
		return err
	}
	if err := s.send(ctx, blackFrames["asus"], 1500*time.Millisecond); err != nil {
		return fmt.Errorf("ASUS initialization frame: %w", err)
	}
	return nil
}

func (s *lcdSession) command(ctx context.Context, name string, body, result any) error {
	seq := s.sequence
	s.sequence++
	report, err := aigoCommand(name, seq, time.Now().UnixMilli(), body)
	if err != nil {
		return err
	}
	if err = s.raw.Write(ctx, report); err != nil {
		return fmt.Errorf("Aigo %s: %w", name, err)
	}
	reply, err := s.raw.Read(ctx, 2500*time.Millisecond)
	if err != nil {
		return fmt.Errorf("Aigo %s: %w", name, err)
	}
	content, err := aigoResponse(reply, seq+1)
	if err != nil {
		return fmt.Errorf("Aigo %s: %w", name, err)
	}
	if result != nil {
		return json.Unmarshal(content, result)
	}
	return nil
}

func aigoCommand(name string, seq uint64, now int64, body any) ([]byte, error) {
	var text bytes.Buffer
	fmt.Fprintf(&text, "POST %s 1\r\nSeqNumber=%d\r\nDate=%d\r\n", name, seq, now)
	var content []byte
	if body != nil {
		var err error
		content, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&text, "ContentType=json\r\nContentLength=%d\r\n", len(content))
	}
	text.WriteString("\r\n")
	text.Write(content)
	return aigoEnvelope(text.Bytes()), nil
}

func aigoEnvelope(payload []byte) []byte {
	middle := binary.BigEndian.AppendUint16(nil, uint16(len(payload)+5))
	middle = append(middle, payload...)
	var sum byte
	for _, b := range middle {
		sum += b
	}
	middle = append(middle, sum)
	out := []byte{0, 0x5a}
	for _, b := range middle {
		switch b {
		case 0x5a:
			out = append(out, 0x5b, 1)
		case 0x5b:
			out = append(out, 0x5b, 2)
		default:
			out = append(out, b)
		}
	}
	return append(out, 0x5a)
}

func aigoResponse(report []byte, ack uint64) ([]byte, error) {
	i := bytes.IndexByte(report, 0x5a)
	if i < 0 {
		return nil, errors.New("missing response frame")
	}
	var data []byte
	ended := false
	for i++; i < len(report); i++ {
		b := report[i]
		if b == 0x5a {
			ended = true
			break
		}
		if b == 0x5b {
			i++
			if i >= len(report) || (report[i] != 1 && report[i] != 2) {
				return nil, errors.New("invalid response escape")
			}
			b = 0x59 + report[i]
		}
		data = append(data, b)
	}
	if !ended || len(data) < 4 || int(binary.BigEndian.Uint16(data)) != len(data)+2 {
		return nil, errors.New("incomplete response")
	}
	var sum byte
	for _, b := range data[:len(data)-1] {
		sum += b
	}
	if sum != data[len(data)-1] {
		return nil, errors.New("response checksum mismatch")
	}
	head, body, ok := bytes.Cut(data[2:len(data)-1], []byte("\r\n\r\n"))
	if !ok {
		return nil, errors.New("missing response headers")
	}
	lines := strings.Split(string(head), "\r\n")
	status := strings.Fields(lines[0])
	if len(status) < 2 || status[1] != "200" {
		return nil, fmt.Errorf("unexpected Aigo status: %s", lines[0])
	}
	for _, line := range lines[1:] {
		if value, ok := strings.CutPrefix(line, "AckNumber="); ok && value == strconv.FormatUint(ack, 10) {
			return body, nil
		}
	}
	return nil, errors.New("unexpected response sequence")
}

func validJPEG(jpeg []byte) bool {
	return len(jpeg) >= 4 && jpeg[0] == 0xff && jpeg[1] == 0xd8 && jpeg[len(jpeg)-2] == 0xff && jpeg[len(jpeg)-1] == 0xd9
}

func sendImageReports(id string, jpeg []byte, write func([]byte) error) error {
	if !validJPEG(jpeg) {
		return errors.New("invalid JPEG frame")
	}
	if id == "aigo" {
		count := (len(jpeg) + 999) / 1000
		if count > 65535 {
			return errors.New("Aigo JPEG is too large")
		}
		var packet [1025]byte
		for i := 0; i < count; i++ {
			chunk := jpeg[i*1000 : min((i+1)*1000, len(jpeg))]
			out := packet[:25+len(chunk)]
			out[1] = 0x5c
			binary.BigEndian.PutUint16(out[2:], uint16(len(out)-4))
			out[4] = 8
			binary.BigEndian.PutUint16(out[5:], uint16(count))
			binary.BigEndian.PutUint16(out[7:], uint16(i))
			out[9] = 1
			copy(out[25:], chunk)
			if err := write(out); err != nil {
				return err
			}
		}
	} else {
		if len(jpeg) > asusMaxJPEG {
			return fmt.Errorf("ASUS JPEG exceeds %d bytes", asusMaxJPEG)
		}
		count := (len(jpeg) + 1019) / 1020
		var packet [1025]byte
		for i := 0; i < count; i++ {
			clear(packet[:])
			packet[1] = 8
			if i == 0 {
				binary.LittleEndian.PutUint16(packet[2:], uint16(count))
				packet[4] = 0x80
			} else {
				binary.LittleEndian.PutUint16(packet[2:], uint16(i))
			}
			copy(packet[5:], jpeg[i*1020:min((i+1)*1020, len(jpeg))])
			if err := write(packet[:]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *lcdSession) send(ctx context.Context, jpeg []byte, timeout time.Duration) error {
	if s.id == "asus" {
		for i := 0; i < 64; i++ {
			_, err := s.raw.Read(ctx, 0)
			if errors.Is(err, errReportTimeout) {
				break
			}
			if err != nil {
				return err
			}
		}
	}
	if err := sendImageReports(s.id, jpeg, func(b []byte) error { return s.raw.Write(ctx, b) }); err != nil {
		return err
	}
	if s.id == "aigo" {
		return nil
	}
	ack, err := s.raw.Read(ctx, timeout)
	if err != nil {
		return fmt.Errorf("ASUS frame acknowledgement: %w", err)
	}
	want := [16]byte{8, 0x81}
	if !bytes.Equal(ack, want[:]) {
		return fmt.Errorf("unexpected ASUS frame acknowledgement: %x", ack)
	}
	return nil
}
