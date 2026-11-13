package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type stats struct {
	CPU               *float64 `json:"cpu"`
	Temperature       *float64 `json:"cpuTemperature"`
	TemperatureSource string   `json:"temperatureSource"`
	TemperatureError  *string  `json:"temperatureError"`
	GPUName           string   `json:"gpuName"`
	GPU               *float64 `json:"gpu"`
	VRAM              *float64 `json:"vram"`
	VRAMUsed          *float64 `json:"vramUsedBytes"`
	VRAMTotal         *float64 `json:"vramTotalBytes"`
	GPUSource         string   `json:"gpuSource"`
	GPUError          *string  `json:"gpuError"`
	Memory            *float64 `json:"memory"`
	MemoryUsed        *float64 `json:"memoryUsedBytes"`
	MemoryTotal       *float64 `json:"memoryTotalBytes"`
	Uptime            float64  `json:"uptime"`
}

type reading struct {
	GPUName     string   `json:"gpuName"`
	GPU         *float64 `json:"gpu"`
	VRAM        *float64 `json:"vram"`
	VRAMUsed    *float64 `json:"vramUsedBytes"`
	VRAMTotal   *float64 `json:"vramTotalBytes"`
	Temperature *float64 `json:"cpuTemperature"`
	Source      string   `json:"source"`
	Error       *string  `json:"error"`
	at          time.Time
}

type telemetry struct {
	mu        sync.RWMutex
	cpu       *float64
	gpu, temp reading
	wg        sync.WaitGroup
}

var (
	kernelDLL    = windows.NewLazySystemDLL("kernel32.dll")
	systemTimes  = kernelDLL.NewProc("GetSystemTimes")
	memoryStatus = kernelDLL.NewProc("GlobalMemoryStatusEx")
	getTick      = kernelDLL.NewProc("GetTickCount64")
)

func backgroundCommand(ctx context.Context, exe string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd
}

func (t *telemetry) start(ctx context.Context, root string) {
	for _, spec := range []struct {
		file   string
		target *reading
	}{
		{"gpu-reader.ps1", &t.gpu}, {"temperature-reader.ps1", &t.temp},
	} {
		t.wg.Go(func() {
			for ctx.Err() == nil {
				err := t.reader(ctx, root, spec.file, spec.target)
				if ctx.Err() != nil {
					return
				}
				message := "Sensor reader stopped"
				if err != nil {
					message = err.Error()
				}
				t.mu.Lock()
				spec.target.at = time.Time{}
				spec.target.Error = &message
				t.mu.Unlock()
				log.Printf("%s: %s", spec.file, message)
				if sleepContext(ctx, 5*time.Second) != nil {
					return
				}
			}
		})
	}
	t.wg.Go(func() {
		idle, total, ok := cpuTicks()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				newIdle, newTotal, valid := cpuTicks()
				if ok && valid && newTotal > total && newIdle >= idle {
					value := max(0, min(100, 100*(1-float64(newIdle-idle)/float64(newTotal-total))))
					t.mu.Lock()
					t.cpu = &value
					t.mu.Unlock()
				} else if !valid {
					t.mu.Lock()
					t.cpu = nil
					t.mu.Unlock()
				}
				idle, total, ok = newIdle, newTotal, valid
			}
		}
	})
}

func (t *telemetry) reader(ctx context.Context, root, file string, target *reading) error {
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := backgroundCommand(ctx, exe, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", filepath.Join(root, file))
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		var sample reading
		if json.Unmarshal(scanner.Bytes(), &sample) != nil {
			continue
		}
		sample.at = time.Now()
		t.mu.Lock()
		*target = sample
		t.mu.Unlock()
	}
	if scanner.Err() != nil {
		_ = cmd.Process.Kill()
	}
	return cmd.Wait()
}

func cpuTicks() (idle, total uint64, ok bool) {
	var i, k, u windows.Filetime
	r, _, _ := systemTimes.Call(uintptr(unsafe.Pointer(&i)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u)))
	as64 := func(ft windows.Filetime) uint64 { return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime) }
	return as64(i), as64(k) + as64(u), r != 0
}

func numberOK(n *float64) *float64 {
	if n == nil || math.IsNaN(*n) || math.IsInf(*n, 0) || *n < 0 {
		return nil
	}
	return n
}

func (t *telemetry) snapshot(now time.Time) stats {
	t.mu.RLock()
	gpu, temp, cpu := t.gpu, t.temp, t.cpu
	t.mu.RUnlock()
	s := stats{CPU: cpu, GPUName: gpu.GPUName, GPUSource: "Windows GPU counters / DXGI", TemperatureSource: defaultString(temp.Source, "ASUS ACPI"), GPUError: gpu.Error, TemperatureError: temp.Error}
	if now.Sub(gpu.at) < 10*time.Second && gpu.Error == nil {
		s.GPU, s.VRAM, s.VRAMUsed, s.VRAMTotal = numberOK(gpu.GPU), numberOK(gpu.VRAM), numberOK(gpu.VRAMUsed), numberOK(gpu.VRAMTotal)
	} else if s.GPUError == nil {
		msg := "GPU data is stale"
		s.GPUError = &msg
	}
	if now.Sub(temp.at) < 10*time.Second && temp.Error == nil {
		s.Temperature = numberOK(temp.Temperature)
	} else if s.TemperatureError == nil {
		msg := "Temperature data is stale"
		s.TemperatureError = &msg
	}
	var m struct {
		size, load                                                               uint32
		total, avail, pageTotal, pageAvail, virtualTotal, virtualAvail, extended uint64
	}
	m.size = uint32(unsafe.Sizeof(m))
	if r, _, _ := memoryStatus.Call(uintptr(unsafe.Pointer(&m))); r != 0 && m.total != 0 {
		used, total := float64(m.total-m.avail), float64(m.total)
		value := 100 * used / total
		s.Memory, s.MemoryUsed, s.MemoryTotal = &value, &used, &total
	}
	ms, _, _ := getTick.Call()
	s.Uptime = float64(ms) / 1000
	return s
}
