// Package qubesdb reads the proxy's config from the per-qube features dom0
// sets (vm-config.torii*), as QubesDB shows them inside the qube under
// /vm-config/, and watches them for changes.
//
// QubesDB refuses values of 3072 bytes or more, so dom0 stores the config
// gzipped, base64'd and split: /vm-config/<key> holds "<parts> <sha256 of
// the JSON>" and /vm-config/<key>.0 .. <key>.<parts-1> the pieces. A
// config whose hash does not match (dom0 is half-way through writing it)
// is an error, never a partial config.
package qubesdb

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Reader reads one QubesDB path; Read is qubesdb-read in production.
type Reader func(path string) ([]byte, error)

// Read runs qubesdb-read.
func Read(path string) ([]byte, error) {
	out, err := exec.Command("qubesdb-read", path).Output()
	if err != nil {
		return nil, fmt.Errorf("qubesdb-read %s: %w", path, err)
	}
	return out, nil
}

// Config returns the JSON stored under /vm-config/<key>.
//
//charter:realizes SW-261127-XGDY
func Config(read Reader, key string) ([]byte, error) {
	head, err := read("/vm-config/" + key)
	if err != nil {
		return nil, err
	}
	f := strings.Fields(string(head))
	if len(f) != 2 {
		return nil, fmt.Errorf("/vm-config/%s: want \"<parts> <sha256>\"", key)
	}
	n, err := strconv.Atoi(f[0])
	if err != nil || n < 1 || n > 1000 {
		return nil, fmt.Errorf("/vm-config/%s: bad part count %q", key, f[0])
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		p, err := read(fmt.Sprintf("/vm-config/%s.%d", key, i))
		if err != nil {
			return nil, err
		}
		b.Write(bytes.TrimSpace(p))
	}
	return Decode(b.String(), f[1])
}

// Decode is the inverse of the dom0 tool's encode_chunks.
func Decode(b64, sha string) ([]byte, error) {
	z, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("config: base64: %w", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, fmt.Errorf("config: gzip: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(zr, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("config: gzip: %w", err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != strings.ToLower(sha) {
		return nil, fmt.Errorf("config: hash mismatch (dom0 may be writing it)")
	}
	return raw, nil
}

// Watch calls changed each time something under /vm-config/ changes,
// until ctx ends or qubesdb-watch exits.
func Watch(ctx context.Context, changed func()) error {
	cmd := exec.CommandContext(ctx, "qubesdb-watch", "/vm-config/")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		changed()
	}
	return cmd.Wait()
}
