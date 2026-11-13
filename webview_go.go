package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type webRenderer struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	lines   chan string
	done    chan struct{}
	reading chan struct{}
	id      string
	config  screenConfig
}

func newWebRenderer(ctx context.Context, root, data, id string, c screenConfig) (*webRenderer, error) {
	exe := filepath.Join(root, "data", "webview-bin", "WebViewRenderer.exe")
	if _, err := os.Stat(exe); err != nil {
		return nil, errors.New("WebView2 helper missing; run build-webview.ps1 for custom pages")
	}
	width, height := dimensions(id)
	// This helper lives across individual capture requests; close owns its lifetime.
	cmd := backgroundCommand(context.Background(), exe, id, filepath.Join(data, "webview"), strconv.Itoa(width), strconv.Itoa(height))
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	r := &webRenderer{cmd: cmd, stdin: in, lines: make(chan string, 2), done: make(chan struct{}), reading: make(chan struct{}), id: id, config: c}
	go func() {
		defer close(r.done)
		defer cmd.Wait()
		defer close(r.lines)
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		for scanner.Scan() {
			select {
			case r.lines <- scanner.Text():
			case <-r.reading:
				return
			}
		}
		if scanner.Err() != nil {
			_ = cmd.Process.Kill()
		}
	}()
	wait, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	if _, err := r.ask(wait, c.URL, "READY"); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

func (r *webRenderer) ask(ctx context.Context, request, prefix string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := io.WriteString(r.stdin, request+"\n"); err != nil {
		return "", fmt.Errorf("WebView2 request: %w", err)
	}
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("WebView2 response: %w", ctx.Err())
	case line, ok := <-r.lines:
		if !ok {
			return "", errors.New("WebView2 helper exited")
		}
		if detail, ok := strings.CutPrefix(line, "ERROR "); ok {
			b, _ := base64.StdEncoding.DecodeString(detail)
			return "", fmt.Errorf("WebView2: %s", b)
		}
		if body, ok := strings.CutPrefix(line, prefix); ok {
			return body, nil
		}
		return "", errors.New("unexpected WebView2 response")
	}
}

func (r *webRenderer) Capture(ctx context.Context, _ stats, _ time.Time) ([]byte, error) {
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	line, err := r.ask(wait, "CAPTURE "+strconv.Itoa(r.config.Quality), "FRAME ")
	if err != nil {
		return nil, err
	}
	var response struct{ Data []byte }
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		return nil, err
	}
	if !validJPEG(response.Data) {
		return nil, errors.New("WebView2 returned an invalid JPEG")
	}
	if r.id == "aigo" {
		return response.Data, nil
	}
	img, err := jpeg.Decode(bytes.NewReader(response.Data))
	if err != nil {
		return nil, err
	}
	return encodeJPEG(img, r.config.Quality, r.config.Rotation, true)
}

func (r *webRenderer) Close() {
	_ = r.stdin.Close()
	close(r.reading)
	select {
	case <-r.done:
	case <-time.After(600 * time.Millisecond):
		_ = r.cmd.Process.Kill()
		<-r.done
	}
}
