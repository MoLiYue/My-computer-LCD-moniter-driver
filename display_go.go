package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"
)

type screenState struct {
	ID                   string         `json:"id"`
	Name                 string         `json:"name"`
	Width                int            `json:"width"`
	Height               int            `json:"height"`
	Config               screenConfig   `json:"config"`
	Running              bool           `json:"running"`
	Connected            bool           `json:"connected"`
	Initialized          bool           `json:"initialized,omitempty"`
	Sent                 uint64         `json:"sent"`
	Bytes                uint64         `json:"bytes"`
	ActualFPS            float64        `json:"actualFps"`
	Retries              uint64         `json:"retries"`
	Reconnects           uint64         `json:"reconnects"`
	SessionResets        uint64         `json:"sessionResets"`
	Reconnecting         bool           `json:"reconnecting"`
	RetryInMS            int64          `json:"retryInMs"`
	Error                string         `json:"error"`
	LastError            string         `json:"lastError,omitempty"`
	LastErrorAt          *time.Time     `json:"lastErrorAt,omitempty"`
	LastTransportError   string         `json:"lastTransportError,omitempty"`
	LastTransportErrorAt *time.Time     `json:"lastTransportErrorAt,omitempty"`
	LastFrameAt          *time.Time     `json:"lastFrameAt,omitempty"`
	Device               map[string]any `json:"device"`
	PowerState           string         `json:"powerState,omitempty"`
	RendererBackend      string         `json:"rendererBackend"`
	WorkerVersion        string         `json:"workerVersion"`
}

type display struct {
	id, root, data, base string
	stats                *telemetry
	actions              sync.Mutex
	mu                   sync.RWMutex
	state                screenState
	frame                []byte
	wanted               bool // guarded by actions
	cancel               context.CancelFunc
	done                 chan struct{}
	parked               *lcdSession // handed off by run after cancellation; read only after done
	openLCD              func(context.Context, string) (*lcdSession, error)
	newSource            func(context.Context, screenConfig) (frameSource, error)
}

func newDisplay(id, root, data, base string, c screenConfig, stats *telemetry) *display {
	w, h := dimensions(id)
	name := "爱国者长条屏"
	if id == "asus" {
		name = "华硕水冷屏"
	}
	d := &display{id: id, root: root, data: data, base: base, stats: stats, state: screenState{ID: id, Name: name, Width: w, Height: h, Config: c, RendererBackend: backend(base, id, c), WorkerVersion: version}, openLCD: connectLCD}
	d.newSource = func(ctx context.Context, c screenConfig) (frameSource, error) {
		if useNative(d.base, d.id, c) {
			return newNativeRenderer(d.id, c)
		}
		return newWebRenderer(ctx, d.root, d.data, d.id, c)
	}
	return d
}

func backend(base, id string, c screenConfig) string {
	if useNative(base, id, c) {
		return "native-go"
	}
	return "webview2"
}

func (d *display) snapshot() screenState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.state
}

func (d *display) preview() []byte {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.frame
}

func (d *display) update(f func(*screenState)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f(&d.state)
}

func (d *display) start(c screenConfig) {
	d.actions.Lock()
	defer d.actions.Unlock()
	if d.wanted && d.snapshot().Config == c {
		return
	}
	d.halt(false)
	d.launch(c)
}

func (d *display) configure(c screenConfig) {
	d.actions.Lock()
	defer d.actions.Unlock()
	if c == d.snapshot().Config {
		return
	}
	resume := d.wanted
	d.halt(false)
	d.update(func(s *screenState) { s.Config = c; s.RendererBackend = backend(d.base, d.id, c); s.Error = "" })
	if resume {
		d.launch(c)
	}
}

func (d *display) launch(c screenConfig) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	d.cancel, d.done, d.wanted = cancel, done, true
	d.update(func(s *screenState) {
		s.Config = c
		s.RendererBackend = backend(d.base, d.id, c)
		s.Error = ""
		s.Reconnecting = true
	})
	go func() { defer close(done); d.run(ctx, c) }()
}

func (d *display) stop() {
	d.actions.Lock()
	defer d.actions.Unlock()
	d.halt(false)
}

func (d *display) halt(keep bool) *lcdSession {
	d.wanted = false
	if d.cancel != nil {
		d.cancel()
		<-d.done
		d.cancel, d.done = nil, nil
	}
	session := d.parked
	d.parked = nil
	d.update(func(s *screenState) {
		s.Running = false
		s.Connected = false
		s.Initialized = false
		s.Reconnecting = false
		s.RetryInMS = 0
		s.ActualFPS = 0
	})
	if keep {
		return session
	}
	session.close()
	return nil
}

func (d *display) run(ctx context.Context, c screenConfig) {
	delay := time.Second
	for ctx.Err() == nil {
		active, err := d.stream(ctx, c)
		if ctx.Err() != nil {
			return
		}
		if active {
			delay = time.Second
		}
		now := time.Now()
		d.update(func(s *screenState) {
			s.Running = false
			s.Connected = false
			s.Initialized = false
			s.ActualFPS = 0
			s.Reconnecting = true
			s.RetryInMS = delay.Milliseconds()
			s.Error = err.Error()
			s.LastError = s.Error
			s.LastErrorAt = &now
		})
		log.Printf("%s: %v; retrying in %s", d.id, err, delay)
		if sleepContext(ctx, delay) != nil {
			return
		}
		d.update(func(s *screenState) { s.Reconnects++; s.RetryInMS = 0 })
		delay = min(30*time.Second, delay*2)
	}
}

func (d *display) stream(ctx context.Context, c screenConfig) (bool, error) {
	// Open the renderer first. A bad custom URL should not flash a healthy LCD black.
	source, err := d.newSource(ctx, c)
	if err != nil {
		return false, err
	}
	defer source.Close()
	session, err := d.openLCD(ctx, d.id)
	if err != nil {
		return false, err
	}
	defer func() {
		if ctx.Err() != nil {
			d.parked = session
		} else {
			session.close()
		}
	}()
	d.update(func(s *screenState) { s.Device = session.device; s.Connected = true; s.Initialized = d.id == "asus" })
	interval := time.Duration(float64(time.Second) / c.FPS)
	active := false
	for ctx.Err() == nil {
		start := time.Now()
		jpeg, err := source.Capture(ctx, d.stats.snapshot(start), start)
		if err != nil {
			return active, err
		}
		if err := d.upload(ctx, session, jpeg); err != nil {
			return active, err
		}
		active = true
		d.publishFrame(jpeg, math.Round(float64(time.Second)/float64(max(interval, time.Since(start)))*10)/10, "on")
		if err := sleepContext(ctx, max(time.Millisecond, interval-time.Since(start))); err != nil {
			return active, err
		}
	}
	return active, ctx.Err()
}

func (d *display) upload(ctx context.Context, session *lcdSession, jpeg []byte) error {
	if d.id == "aigo" {
		return session.send(ctx, jpeg, 0)
	}
	for attempt := 0; ; attempt++ {
		err := session.send(ctx, jpeg, 1500*time.Millisecond)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil || attempt == 2 {
			return err
		}
		now := time.Now()
		d.update(func(s *screenState) {
			s.Retries++
			s.Initialized = false
			s.LastTransportError = err.Error()
			s.LastTransportErrorAt = &now
		})
		if err := sleepContext(ctx, 100*time.Millisecond); err != nil {
			return err
		}
		if err := session.initialize(ctx); err != nil {
			return fmt.Errorf("ASUS session reset: %w", err)
		}
		d.update(func(s *screenState) { s.SessionResets++; s.Initialized = true })
		log.Printf("asus: recovered upload session after %v", err)
	}
}

func (d *display) publishFrame(jpeg []byte, fps float64, power string) {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frame = jpeg
	d.state.Sent++
	d.state.Bytes += uint64(len(jpeg))
	d.state.LastFrameAt = &now
	d.state.PowerState = power
	d.state.Error = ""
	d.state.ActualFPS = fps
	d.state.Running = power == "on"
	d.state.Reconnecting = false
	d.state.RetryInMS = 0
}

func (d *display) powerOff(ctx context.Context) error {
	d.actions.Lock()
	defer d.actions.Unlock()
	session := d.halt(true)
	state := d.snapshot()
	if session == nil && (state.Device == nil || state.PowerState == "black" || state.PowerState == "suspended") {
		return nil
	}
	if err := ctx.Err(); err != nil {
		session.close()
		return err
	}
	if session == nil {
		var err error
		session, err = d.openLCD(ctx, d.id)
		if err != nil {
			return fmt.Errorf("reopen LCD to blank: %w", err)
		}
	}
	defer session.close()
	black := blackFrames[d.id]
	err := session.send(ctx, black, 700*time.Millisecond)
	if err != nil {
		d.update(func(s *screenState) { s.Error = "Power off: " + err.Error() })
		return err
	}
	d.publishFrame(black, 0, "black")
	if d.id == "aigo" {
		if err := session.command(ctx, "power", map[string]string{"event": "suspend"}, nil); err != nil {
			d.update(func(s *screenState) { s.Error = "Power off: " + err.Error() })
			return err
		}
		d.update(func(s *screenState) { s.PowerState = "suspended" })
	}
	return nil
}

func checkDisplayID(id string) error {
	if id != "aigo" && id != "asus" {
		return errors.New("unknown LCD")
	}
	return nil
}
