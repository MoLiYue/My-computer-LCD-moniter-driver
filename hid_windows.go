package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	hidDLL           = windows.NewLazySystemDLL("hid.dll")
	hidGuid          = hidDLL.NewProc("HidD_GetHidGuid")
	hidAttrs         = hidDLL.NewProc("HidD_GetAttributes")
	hidPrepare       = hidDLL.NewProc("HidD_GetPreparsedData")
	hidFree          = hidDLL.NewProc("HidD_FreePreparsedData")
	hidCaps          = hidDLL.NewProc("HidP_GetCaps")
	hidSerial        = hidDLL.NewProc("HidD_GetSerialNumberString")
	hidProduct       = hidDLL.NewProc("HidD_GetProductString")
	setupDLL         = windows.NewLazySystemDLL("setupapi.dll")
	enumInterface    = setupDLL.NewProc("SetupDiEnumDeviceInterfaces")
	interfaceDetail  = setupDLL.NewProc("SetupDiGetDeviceInterfaceDetailW")
	errReportTimeout = errors.New("HID input report timed out")
)

type hidDevice struct {
	path, serial   string
	iface          int
	usage, in, out uint16
}

type interfaceData struct {
	size     uint32
	guid     windows.GUID
	flags    uint32
	reserved uintptr
}

type hidAttributes struct {
	size                     uint32
	vendor, product, version uint16
}

func findHID(vendor, product, usage uint16) ([]hidDevice, error) {
	var guid windows.GUID
	hidGuid.Call(uintptr(unsafe.Pointer(&guid)))
	set, err := windows.SetupDiGetClassDevsEx(&guid, "", 0, windows.DIGCF_PRESENT|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return nil, err
	}
	defer windows.SetupDiDestroyDeviceInfoList(set)
	match := fmt.Sprintf("vid_%04x&pid_%04x", vendor, product)
	var found []hidDevice
	for i := uint32(0); ; i++ {
		info := interfaceData{size: uint32(unsafe.Sizeof(interfaceData{}))}
		r, _, err := enumInterface.Call(uintptr(set), 0, uintptr(unsafe.Pointer(&guid)), uintptr(i), uintptr(unsafe.Pointer(&info)))
		if r == 0 {
			if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
				break
			}
			return nil, err
		}
		var n uint32
		interfaceDetail.Call(uintptr(set), uintptr(unsafe.Pointer(&info)), 0, 0, uintptr(unsafe.Pointer(&n)), 0)
		if n < 8 || n > 65536 {
			continue
		}
		buf := make([]uint16, (n+1)/2)
		buf[0] = 8 // SP_DEVICE_INTERFACE_DETAIL_DATA_W on x64
		r, _, err = interfaceDetail.Call(uintptr(set), uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&buf[0])), uintptr(n), 0, 0)
		if r == 0 {
			return nil, err
		}
		path := windows.UTF16ToString(buf[2:]) // DevicePath begins four bytes after cbSize
		if !strings.Contains(strings.ToLower(path), match) {
			continue
		}
		device, err := describeHID(path, vendor, product, usage)
		if err != nil {
			return nil, fmt.Errorf("inspect LCD interface: %w", err)
		}
		if device != nil {
			found = append(found, *device)
		}
	}
	return found, nil
}

func describeHID(path string, vendor, product, usage uint16) (*hidDevice, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)
	a := hidAttributes{size: uint32(unsafe.Sizeof(hidAttributes{}))}
	r, _, err := hidAttrs.Call(uintptr(h), uintptr(unsafe.Pointer(&a)))
	if r == 0 {
		return nil, err
	}
	if a.vendor != vendor || a.product != product {
		return nil, nil
	}
	var preparsed uintptr
	r, _, err = hidPrepare.Call(uintptr(h), uintptr(unsafe.Pointer(&preparsed)))
	if r == 0 {
		return nil, err
	}
	defer hidFree.Call(preparsed)
	var caps [32]uint16
	r, _, _ = hidCaps.Call(preparsed, uintptr(unsafe.Pointer(&caps[0])))
	if uint32(r) != 0x00110000 {
		return nil, fmt.Errorf("HidP_GetCaps returned %#x", r)
	}
	if caps[1] != usage {
		return nil, nil
	}
	if vendor == 0x1d6b && product == 0x0103 {
		var label [128]uint16
		if ok, _, _ := hidProduct.Call(uintptr(h), uintptr(unsafe.Pointer(&label[0])), uintptr(len(label)*2)); ok == 0 || windows.UTF16ToString(label[:]) != "Aigo USB Device" {
			return nil, nil
		}
	}
	device := hidDevice{path: path, iface: -1, usage: caps[1], in: caps[2], out: caps[3]}
	lower := strings.ToLower(path)
	if strings.Contains(lower, "&mi_00") {
		device.iface = 0
	}
	if strings.Contains(lower, "&mi_01") {
		device.iface = 1
	}
	var serial [128]uint16
	if ok, _, _ := hidSerial.Call(uintptr(h), uintptr(unsafe.Pointer(&serial[0])), uintptr(len(serial)*2)); ok != 0 {
		device.serial = windows.UTF16ToString(serial[:])
	}
	return &device, nil
}

type reportPort interface {
	Write(context.Context, []byte) error
	Read(context.Context, time.Duration) ([]byte, error)
	Close()
}

// Only the owning display goroutine uses this handle. A timed-out read stays
// pending (as with HIDAPI), so draining old input does not race the next ACK.
type hidPort struct {
	h           windows.Handle
	in, out     []byte
	read, write windows.Overlapped
	pending     bool
	pin         runtime.Pinner
}

func openHID(dev hidDevice) (reportPort, error) {
	if dev.in < 2 || dev.out < 2 {
		return nil, errors.New("LCD interface has invalid HID report sizes")
	}
	name, err := windows.UTF16PtrFromString(dev.path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	p := &hidPort{h: h, in: make([]byte, dev.in), out: make([]byte, dev.out)}
	p.read.HEvent, err = windows.CreateEvent(nil, 1, 0, nil)
	if err == nil {
		p.write.HEvent, err = windows.CreateEvent(nil, 1, 0, nil)
	}
	if err != nil {
		p.Close()
		return nil, err
	}
	p.pin.Pin(&p.in[0])
	p.pin.Pin(&p.out[0])
	p.pin.Pin(&p.read)
	p.pin.Pin(&p.write)
	return p, nil
}

func waitReport(ctx context.Context, event windows.Handle, wait time.Duration) error {
	until := time.Now().Add(wait)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := max(time.Until(until), 0)
		ms := uint32((min(remaining, 25*time.Millisecond) + time.Millisecond - 1) / time.Millisecond)
		result, err := windows.WaitForSingleObject(event, ms)
		if err != nil {
			return err
		}
		if result == windows.WAIT_OBJECT_0 {
			return nil
		}
		if result != uint32(windows.WAIT_TIMEOUT) {
			return fmt.Errorf("unexpected HID wait result %#x", result)
		}
		if !time.Now().Before(until) {
			return errReportTimeout
		}
	}
}

func cancelReport(h windows.Handle, o *windows.Overlapped) {
	_ = windows.CancelIoEx(h, o)
	var n uint32
	_ = windows.GetOverlappedResult(h, o, &n, true)
}

func (p *hidPort) Write(ctx context.Context, b []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(b) > len(p.out) {
		return fmt.Errorf("report exceeds HID output size (%d > %d)", len(b), len(p.out))
	}
	clear(p.out)
	copy(p.out, b)
	if err := windows.ResetEvent(p.write.HEvent); err != nil {
		return err
	}
	var n uint32
	err := windows.WriteFile(p.h, p.out, &n, &p.write)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		if err = waitReport(ctx, p.write.HEvent, time.Second); err != nil {
			cancelReport(p.h, &p.write)
			return fmt.Errorf("HID output: %w", err)
		}
		err = windows.GetOverlappedResult(p.h, &p.write, &n, false)
	}
	if err != nil {
		return err
	}
	if int(n) != len(p.out) {
		return fmt.Errorf("short HID write (%d of %d bytes)", n, len(p.out))
	}
	return nil
}

func (p *hidPort) Read(ctx context.Context, wait time.Duration) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var n uint32
	if !p.pending {
		if err := windows.ResetEvent(p.read.HEvent); err != nil {
			return nil, err
		}
		err := windows.ReadFile(p.h, p.in, &n, &p.read)
		if err == nil {
			return reportPayload(p.in[:n]), nil
		}
		if !errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, err
		}
		p.pending = true
	}
	if err := waitReport(ctx, p.read.HEvent, wait); err != nil {
		return nil, err
	}
	err := windows.GetOverlappedResult(p.h, &p.read, &n, false)
	p.pending = false
	if err != nil {
		return nil, err
	}
	return reportPayload(p.in[:n]), nil
}

func reportPayload(b []byte) []byte {
	if len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	return b
}

func (p *hidPort) Close() {
	if p.pending {
		cancelReport(p.h, &p.read)
		p.pending = false
	}
	if p.read.HEvent != 0 {
		windows.CloseHandle(p.read.HEvent)
	}
	if p.write.HEvent != 0 {
		windows.CloseHandle(p.write.HEvent)
	}
	windows.CloseHandle(p.h)
	p.pin.Unpin()
}
