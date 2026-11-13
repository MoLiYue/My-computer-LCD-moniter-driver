package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type hub struct {
	base, host, data string
	displays         map[string]*display
	sensors          telemetry
	watcher          sessionWatcher
	actions          sync.Mutex
	settings         map[string]screenConfig
	ending           bool // guarded by actions
	ended            chan struct{}
	shutdownOnce     sync.Once
}

type actionResult struct {
	ID    string  `json:"id"`
	OK    bool    `json:"ok"`
	Error *string `json:"error"`
}

type offResult struct {
	OK      bool           `json:"ok"`
	Results []actionResult `json:"results"`
}

func newHub(root, data, host string) *hub {
	base := "http://" + host
	c, err := loadSettings(data, base)
	if err != nil {
		log.Printf("load settings: %v", err)
	}
	h := &hub{base: base, host: host, data: data, settings: c, displays: map[string]*display{}, ended: make(chan struct{})}
	for _, id := range screenIDs {
		h.displays[id] = newDisplay(id, root, data, base, c[id], &h.sensors)
	}
	return h
}

func (h *hub) status() map[string]any {
	screens := map[string]screenState{}
	for _, id := range screenIDs {
		screens[id] = h.displays[id].snapshot()
	}
	return map[string]any{"name": "Miku Screen Hub", "version": version, "engine": "go", "shutdownWatcher": h.watcher.snapshot(), "screens": screens, "telemetry": h.sensors.snapshot(time.Now())}
}

func (h *hub) all(action func(string) error) []actionResult {
	var wg sync.WaitGroup
	out := make([]actionResult, len(screenIDs))
	for i, id := range screenIDs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := action(id)
			out[i] = actionResult{ID: id, OK: err == nil}
			if err != nil {
				msg := err.Error()
				out[i].Error = &msg
			}
		}()
	}
	wg.Wait()
	return out
}

func (h *hub) off(ctx context.Context, reason string) offResult {
	results := h.all(func(id string) error { return h.displays[id].powerOff(ctx) })
	result := offResult{OK: true, Results: results}
	for _, r := range results {
		result.OK = result.OK && r.OK
	}
	entry, _ := json.Marshal(map[string]any{"reason": reason, "results": results})
	f, err := os.OpenFile(filepath.Join(h.data, "shutdown.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339Nano), entry)
		f.Close()
	}
	return result
}

func (h *hub) shutdown(reason string) {
	h.shutdownOnce.Do(func() {
		h.actions.Lock()
		defer h.actions.Unlock()
		defer close(h.ended)
		h.ending = true
		ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
		defer cancel()
		if result := h.off(ctx, reason); !result.OK {
			log.Printf("some LCDs did not blank: %+v", result.Results)
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func apiError(w http.ResponseWriter, status int, err string) {
	writeJSON(w, status, map[string]string{"error": err})
}

func (h *hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Host != h.host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != h.base) {
		apiError(w, http.StatusForbidden, "Local requests only")
		return
	}
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/api/status":
			writeJSON(w, 200, h.status())
			return
		case "/api/stats":
			writeJSON(w, 200, h.sensors.snapshot(time.Now()))
			return
		}
		if file, ok := strings.CutPrefix(r.URL.Path, "/preview/"); ok && strings.HasSuffix(file, ".jpg") {
			d := h.displays[strings.TrimSuffix(file, ".jpg")]
			if d != nil && len(d.preview()) > 0 {
				w.Header().Set("Content-Type", "image/jpeg")
				w.Write(d.preview())
				return
			}
			apiError(w, 404, "Waiting for first frame")
			return
		}
		file := ""
		switch r.URL.Path {
		case "/":
			file = "public/panel.html"
		case "/screens/aigo":
			file = "public/aigo.html"
		case "/screens/asus":
			file = "public/asus.html"
		default:
			if name, ok := strings.CutPrefix(r.URL.Path, "/assets/"); ok && name == path.Base(name) {
				file = "public/assets/" + name
			}
		}
		if b, err := public.ReadFile(file); file != "" && err == nil {
			w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(file)))
			if strings.HasPrefix(file, "public/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=86400")
			}
			w.Write(b)
			return
		}
		apiError(w, 404, "Not found")
		return
	}
	if r.Method != http.MethodPost {
		apiError(w, 404, "Not found")
		return
	}
	// The Go window handles session-end messages directly. Reject the legacy
	// HTTP callback so an unrelated local process cannot impersonate Windows.
	if r.URL.Path == "/api/session-ending" {
		apiError(w, 403, "Session notifications are handled by Windows")
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		apiError(w, 400, "Request too large or unreadable")
		return
	}
	var patch configPatch
	if len(b) > 0 {
		if err := json.Unmarshal(b, &patch); err != nil {
			apiError(w, 400, err.Error())
			return
		}
	}
	h.actions.Lock()
	defer h.actions.Unlock()
	if h.ending {
		apiError(w, 503, "Program is exiting")
		return
	}
	switch r.URL.Path {
	case "/api/exit":
		h.ending = true
		writeJSON(w, 200, map[string]bool{"exiting": true})
		go h.shutdown("program-exit")
		return
	case "/api/power-off-all":
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		writeJSON(w, 200, h.off(ctx, "manual"))
		return
	case "/api/start-all", "/api/stop-all":
		out := h.all(func(id string) error {
			if r.URL.Path == "/api/start-all" {
				h.displays[id].start(h.settings[id])
			} else {
				h.displays[id].stop()
			}
			return nil
		})
		status := h.status()
		status["results"] = out
		writeJSON(w, 200, status)
		return
	}
	route := strings.Split(r.URL.Path, "/")
	if len(route) != 5 || route[1] != "api" || route[2] != "screens" || h.displays[route[3]] == nil {
		apiError(w, 404, "Unknown action")
		return
	}
	id := route[3]
	d := h.displays[id]
	switch route[4] {
	case "start":
		d.start(h.settings[id])
	case "stop":
		d.stop()
	case "config":
		old := h.settings[id]
		c, err := applyConfig(id, old, patch)
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		h.settings[id] = c
		if err := saveSettings(h.data, h.settings); err != nil {
			h.settings[id] = old
			apiError(w, 500, err.Error())
			return
		}
		d.configure(c)
	default:
		apiError(w, 404, "Unknown action")
		return
	}
	writeJSON(w, 200, h.status())
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Dir(exe)
	portDefault := 19330
	if env := os.Getenv("SCREEN_HUB_PORT"); env != "" {
		portDefault, err = strconv.Atoi(env)
		if err != nil {
			return errors.New("invalid SCREEN_HUB_PORT")
		}
	}
	start := flag.Bool("start", false, "start both LCDs")
	open := flag.Bool("open", false, "open the local control panel")
	port := flag.Int("port", portDefault, "local HTTP port")
	dataArg := flag.String("data", "", "separate settings and runtime directory")
	probe := flag.String("probe-hid", "", "list LCD interfaces without uploading a frame")
	flag.Parse()
	if *probe != "" {
		if err := checkDisplayID(*probe); err != nil {
			return err
		}
		vendor, product, usage := uint16(0x0b05), uint16(0x1ca9), uint16(0xff06)
		if *probe == "aigo" {
			vendor, product, usage = 0x1d6b, 0x0103, 12
		}
		devices, err := findHID(vendor, product, usage)
		if err != nil {
			return err
		}
		for _, dev := range devices {
			fmt.Printf("%s interface=%d usage=%04x input=%d output=%d\n", *probe, dev.iface, dev.usage, dev.in, dev.out)
		}
		return nil
	}
	if *port < 1024 || *port > 65535 {
		return errors.New("invalid local port")
	}
	data := *dataArg
	if data == "" {
		data = filepath.Join(root, "data")
	}
	if err := os.MkdirAll(data, 0700); err != nil {
		return err
	}
	host := fmt.Sprintf("127.0.0.1:%d", *port)
	h := newHub(root, data, host)
	ln, err := net.Listen("tcp4", host)
	if err != nil {
		if *open {
			openPanel(h.base)
		}
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.sensors.start(ctx, root)
	if os.Getenv("SCREEN_HUB_NO_SESSION_WATCHER") != "1" {
		go h.watcher.listen(func(reason string) {
			done := make(chan struct{})
			go func() { h.shutdown(reason); close(done) }()
			select {
			case <-done:
			case <-time.After(4 * time.Second):
			}
		})
	}
	server := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 45 * time.Second}
	serveErr := make(chan error, 1)
	log.Printf("Miku Screen Hub %s (Go): %s", version, h.base)
	if *start {
		for _, id := range screenIDs {
			h.displays[id].start(h.settings[id])
		}
	}
	go func() { serveErr <- server.Serve(ln) }()
	if *open {
		openPanel(h.base)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	select {
	case <-h.ended:
	case <-signals:
		h.shutdown("program-exit")
	case err = <-serveErr:
		h.shutdown("server-exit")
	}
	cancel()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	_ = server.Shutdown(closeCtx)
	h.sensors.wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func openPanel(url string) {
	cmd := backgroundCommand(context.Background(), filepath.Join(os.Getenv("SystemRoot"), "System32", "rundll32.exe"), "url.dll,FileProtocolHandler", url)
	if err := cmd.Start(); err == nil {
		go cmd.Wait()
	}
}
