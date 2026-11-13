package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

const version = "2.0.0"

var screenIDs = [...]string{"aigo", "asus"}

type screenConfig struct {
	URL      string  `json:"url"`
	FPS      float64 `json:"fps"`
	Quality  int     `json:"quality"`
	Rotation int     `json:"rotation"`
}

type configPatch struct {
	URL      *string  `json:"url"`
	FPS      *float64 `json:"fps"`
	Quality  *int     `json:"quality"`
	Rotation *int     `json:"rotation"`
}

func dimensions(id string) (int, int) {
	if id == "aigo" {
		return 1920, 462
	}
	return 320, 320
}

func defaults(base, id string) screenConfig {
	c := screenConfig{URL: base + "/screens/" + id, FPS: 5, Quality: 80}
	if id == "asus" {
		c.Rotation = 180
	}
	return c
}

func applyConfig(id string, c screenConfig, p configPatch) (screenConfig, error) {
	if p.URL != nil {
		c.URL = *p.URL
	}
	if p.FPS != nil {
		c.FPS = *p.FPS
	}
	if p.Quality != nil {
		c.Quality = *p.Quality
	}
	if p.Rotation != nil {
		c.Rotation = *p.Rotation
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return c, errors.New("use an HTTP or HTTPS URL without embedded credentials")
	}
	if math.IsNaN(c.FPS) || c.FPS < 1 || c.FPS > 15 || c.Quality < 30 || c.Quality > 95 {
		return c, errors.New("invalid frame rate or quality")
	}
	if c.Rotation != 0 && (id == "aigo" || (c.Rotation != 90 && c.Rotation != 180 && c.Rotation != 270)) {
		return c, errors.New("invalid rotation")
	}
	return c, nil
}

func useNative(base, id string, c screenConfig) bool {
	u, err := url.Parse(c.URL)
	return err == nil && u.Scheme+"://"+u.Host == base && u.Path == "/screens/"+id && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

func loadSettings(data, base string) (map[string]screenConfig, error) {
	c := map[string]screenConfig{}
	for _, id := range screenIDs {
		c[id] = defaults(base, id)
	}
	b, err := os.ReadFile(filepath.Join(data, "config.json"))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	var patches map[string]configPatch
	if err := json.Unmarshal(b, &patches); err != nil {
		return c, err
	}
	for _, id := range screenIDs {
		s, err := applyConfig(id, c[id], patches[id])
		if err != nil {
			return c, fmt.Errorf("%s: %w", id, err)
		}
		c[id] = s
	}
	return c, nil
}

func saveSettings(data string, c map[string]screenConfig) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(data, "config-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, err = f.Write(append(b, '\n'))
	closed := f.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	from, _ := windows.UTF16PtrFromString(tmp)
	to, _ := windows.UTF16PtrFromString(filepath.Join(data, "config.json"))
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
