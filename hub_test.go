package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image/jpeg"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestImageReports(t *testing.T) {
	frame := make([]byte, 2201)
	copy(frame, []byte{0xff, 0xd8})
	copy(frame[len(frame)-2:], []byte{0xff, 0xd9})
	for i := 2; i < len(frame)-2; i++ {
		frame[i] = byte(i)
	}
	for _, id := range screenIDs {
		t.Run(id, func(t *testing.T) {
			var chunks [][]byte
			err := sendImageReports(id, frame, func(report []byte) error { chunks = append(chunks, bytes.Clone(report)); return nil })
			if err != nil || len(chunks) != 3 {
				t.Fatalf("%d reports: %v", len(chunks), err)
			}
			var restored []byte
			for i, b := range chunks {
				if id == "aigo" {
					if b[0] != 0 || b[1] != 0x5c || int(binary.BigEndian.Uint16(b[2:])) != len(b)-4 || binary.BigEndian.Uint16(b[5:]) != 3 || int(binary.BigEndian.Uint16(b[7:])) != i {
						t.Fatalf("bad Aigo header: %x", b[:25])
					}
					restored = append(restored, b[25:]...)
				} else {
					want, flag := i, byte(0)
					if i == 0 {
						want, flag = 3, 0x80
					}
					if len(b) != 1025 || b[0] != 0 || b[1] != 8 || int(binary.LittleEndian.Uint16(b[2:])) != want || b[4] != flag {
						t.Fatalf("bad ASUS header: %x", b[:5])
					}
					restored = append(restored, b[5:]...)
				}
			}
			if !bytes.Equal(restored[:len(frame)], frame) {
				t.Fatal("JPEG did not survive packetization")
			}
		})
	}
}

func TestAigoReply(t *testing.T) {
	raw := []byte("HTTP 200\r\nAckNumber=9\r\n\r\n{\"payload\":\"Z[\"}")
	reply := aigoEnvelope(raw)
	body, err := aigoResponse(reply, 9)
	if err != nil || string(body) != "{\"payload\":\"Z[\"}" {
		t.Fatalf("reply: %s %v", body, err)
	}
	if _, err := aigoResponse(reply, 8); err == nil {
		t.Fatal("accepted wrong sequence")
	}
	reply[12] ^= 1
	if _, err := aigoResponse(reply, 9); err == nil {
		t.Fatal("accepted damaged response")
	}
}

func TestLocalConfig(t *testing.T) {
	const host = "127.0.0.1:19330"
	dir := t.TempDir()
	h := newHub(".", dir, host)
	post := func(path, body, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://"+host+path, strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	for _, req := range []struct {
		path, body, origin string
		status             int
	}{
		{"/api/screens/asus/config", "{\"fps\":0}", "", 400},
		{"/api/screens/asus/config", "{\"url\":\"file:///C:/Windows/\"}", "", 400},
		{"/api/screens/aigo/config", "{\"rotation\":180}", "", 400},
		{"/api/session-ending", "{}", "", 403},
		{"/api/exit", "{}", "https://example.org", 403},
		{"/api/screens/asus/config", "{\"rotation\":90,\"quality\":83}", "", 200},
	} {
		if res := post(req.path, req.body, req.origin); res.Code != req.status {
			t.Fatalf("%s: %d %s", req.path, res.Code, res.Body)
		}
	}
	cfg, err := loadSettings(dir, "http://"+host)
	if err != nil || cfg["asus"].Rotation != 90 || cfg["asus"].Quality != 83 || cfg["aigo"].Quality != 80 {
		t.Fatalf("settings: %v %v", cfg, err)
	}
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, httptest.NewRequest("GET", "http://"+host+"/api/status", nil))
	var status struct {
		Engine  string
		Screens map[string]screenState
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &status); err != nil || status.Engine != "go" || status.Screens["asus"].RendererBackend != "native-go" {
		t.Fatalf("unexpected status: %s", resp.Body)
	}
}

func TestNativeFrames(t *testing.T) {
	v := 47.0
	for _, id := range screenIDs {
		t.Run(id, func(t *testing.T) {
			conf := defaults("http://127.0.0.1:19330", id)
			conf.Quality = 95
			r, err := newNativeRenderer(id, conf)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			now := time.Now()
			a, err := r.Capture(context.Background(), stats{}, now)
			if err != nil {
				t.Fatal(err)
			}
			b, err := r.Capture(context.Background(), stats{CPU: &v, Temperature: &v, GPU: &v, Memory: &v, VRAM: &v}, now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			s, err := jpeg.DecodeConfig(bytes.NewReader(b))
			w, h := dimensions(id)
			if err != nil || s.Width != w || s.Height != h || bytes.Equal(a, b) {
				t.Fatalf("bad frame %v %v", s, err)
			}
			if id == "asus" && len(b) > asusMaxJPEG {
				t.Fatal("ASUS frame over transport limit")
			}
		})
	}
}

type fakeReportPort struct {
	writes [][]byte
	acks   int
	drop   int
	closed bool
}

func (f *fakeReportPort) Write(_ context.Context, b []byte) error {
	f.writes = append(f.writes, bytes.Clone(b))
	return nil
}
func (f *fakeReportPort) Read(_ context.Context, wait time.Duration) ([]byte, error) {
	if wait == 0 {
		return nil, errReportTimeout
	}
	if f.drop > 0 {
		f.drop--
		return nil, errReportTimeout
	}
	f.acks++
	b := make([]byte, 16)
	b[0] = 8
	b[1] = 0x81
	return b, nil
}
func (f *fakeReportPort) Close() { f.closed = true }

func TestASUSLostAckResetsAndRetries(t *testing.T) {
	base := "http://127.0.0.1:19330"
	d := newDisplay("asus", ".", t.TempDir(), base, defaults(base, "asus"), &telemetry{})
	raw, init := &fakeReportPort{drop: 1}, &fakeReportPort{}
	s := &lcdSession{id: "asus", raw: raw, init: init}
	if err := d.upload(context.Background(), s, blackFrames["asus"]); err != nil {
		t.Fatal(err)
	}
	if state := d.snapshot(); state.Retries != 1 || state.SessionResets != 1 || !state.Initialized || raw.acks != 2 || len(init.writes) != 1 {
		t.Fatalf("did not reinitialize, verify and retry: %+v (%d ACKs)", state, raw.acks)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.initialize(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled initialization: %v", err)
	}
}
