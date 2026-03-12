//go:build cgo

// Package focas provides a FOCAS2 client for Fanuc CNC controllers.
//
// Build requirements:
//   - Fanuc FOCAS2 SDK installed (fwlib32.h + libfwlib32.so / fwlib32.dll)
//   - Default SDK paths: /usr/local/include/focas/ and /usr/local/lib/
//   - Override at build time: CGO_CFLAGS="-I/your/path" CGO_LDFLAGS="-L/your/path -lfwlib32"
//
// Architecture note:
//   The Fanuc SDK ships in both 32-bit (libfwlib32.so) and 64-bit (Fwlib64.so) variants.
//   Modern 31i-series controllers support the 64-bit SDK. If you only have the 32-bit
//   library, build with: GOARCH=386 CGO_ENABLED=1 go build ./cmd/focas-logger
package focas

/*
#cgo CFLAGS:  -I/usr/local/include/focas
#cgo LDFLAGS: -L/usr/local/lib -lfwlib32 -Wl,-rpath,/usr/local/lib

#include <stdlib.h>

// FOCAS2 type definitions (mirrors fwlib32.h; included here to avoid
// requiring the header at every build site).
typedef unsigned short FHND;

typedef struct {
	short dummy;
	short tmmode;    // T/M mode
	short aut;       // automatic/manual mode
	short run;       // 0=stop 1=hold 2=start(running) 3=mstr 4=restart ...
	short edit;
	short mst;
	short emergency;
	short alarm;
	short prog;
} ODBST;

typedef struct {
	char name[36];   // executing program name (null-terminated)
	long o_num;      // O number
} ODBEXEPRG;

extern short cnc_allclibhndl3(const char *ipaddr, unsigned short port, long timeout, unsigned short *flibhndl);
extern short cnc_freelibhndl(unsigned short flibhndl);
extern short cnc_statinfo(unsigned short flibhndl, ODBST *statinfo);
extern short cnc_exeprgname(unsigned short flibhndl, ODBEXEPRG *exeprg);
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"
)

const ewOK C.short = 0

// Client is an open FOCAS2 session with a Fanuc controller.
type Client struct {
	h C.ushort
}

// Connect opens a FOCAS2 session to host:port.
// timeoutSecs is the connection timeout; 10 is a reasonable default.
func Connect(host string, port int, timeoutSecs int) (*Client, error) {
	chost := C.CString(host)
	defer C.free(unsafe.Pointer(chost))

	var h C.ushort
	ret := C.cnc_allclibhndl3(chost, C.ushort(port), C.long(timeoutSecs), &h)
	if ret != ewOK {
		return nil, fmt.Errorf("focas connect %s:%d: error %d", host, port, int(ret))
	}
	return &Client{h: h}, nil
}

// Close releases the FOCAS2 session. Safe to call on a nil Client.
func (c *Client) Close() {
	if c != nil {
		C.cnc_freelibhndl(c.h)
	}
}

// IsRunning reports whether the controller is actively executing an NC program.
// ODBST.run == 2 means "STaRT" (program running); all other values are idle states.
// run == 3 (MSTR: manual numerical command) is treated as idle intentionally.
func (c *Client) IsRunning() (bool, error) {
	var st C.ODBST
	ret := C.cnc_statinfo(c.h, &st)
	if ret != ewOK {
		return false, fmt.Errorf("focas statinfo: error %d", int(ret))
	}
	return int(st.run) == 2, nil
}

// ProgramName returns the name of the currently executing NC program.
// Returns an empty string if no program name is available.
func (c *Client) ProgramName() (string, error) {
	var prg C.ODBEXEPRG
	ret := C.cnc_exeprgname(c.h, &prg)
	if ret != ewOK {
		return "", fmt.Errorf("focas exeprgname: error %d", int(ret))
	}
	return strings.TrimSpace(C.GoString(&prg.name[0])), nil
}
