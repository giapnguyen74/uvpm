//go:build linux

package daemon

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// listeners returns the TCP addresses ("*:8000", "127.0.0.1:8000") that any
// process in the process group pgid is listening on.
func listeners(pgid int) []string {
	inodes := groupSocketInodes(pgid)
	if len(inodes) == 0 {
		return nil
	}
	var out []string
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		out = append(out, listenFromTable(f, inodes)...)
	}
	return out
}

func groupSocketInodes(pgid int) map[string]bool {
	inodes := map[string]bool{}
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		b, err := os.ReadFile(d + "/stat")
		if err != nil {
			continue
		}
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:]) // state ppid pgrp ...
		if len(f) < 3 || f[2] != strconv.Itoa(pgid) {
			continue
		}
		fds, _ := os.ReadDir(d + "/fd")
		for _, fd := range fds {
			if l, err := os.Readlink(d + "/fd/" + fd.Name()); err == nil && strings.HasPrefix(l, "socket:[") {
				inodes[strings.TrimSuffix(strings.TrimPrefix(l, "socket:["), "]")] = true
			}
		}
	}
	return inodes
}

func listenFromTable(path string, inodes map[string]bool) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != "0A" || !inodes[f[9]] { // 0A = LISTEN
			continue
		}
		if a := hexAddr(f[1]); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// hexAddr decodes "0100007F:1F90" (or the 32-hex-digit IPv6 form).
func hexAddr(s string) string {
	h, p, ok := strings.Cut(s, ":")
	if !ok {
		return ""
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil || (len(h) != 8 && len(h) != 32) {
		return ""
	}
	ip := make(net.IP, len(h)/2)
	for w := 0; w < len(h); w += 8 { // each 32-bit word is little-endian
		v, err := strconv.ParseUint(h[w:w+8], 16, 32)
		if err != nil {
			return ""
		}
		for i := 0; i < 4; i++ {
			ip[w/2+i] = byte(v >> (8 * i))
		}
	}
	if ip.IsUnspecified() {
		return fmt.Sprintf("*:%d", port)
	}
	return net.JoinHostPort(ip.String(), strconv.FormatUint(port, 10))
}
