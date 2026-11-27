package qubesdb

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/leroux/torii-qubes/claims"
)

// The dom0 tool's own encoder writes the fixture, so the two sides agree.
func TestDecodesWhatDom0Writes(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_XGDY, "chunks-roundtrip", "half-written-refused")
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	script := `import json,sys; sys.path.insert(0, "../../../dom0")
from torii import derive
import hashlib
cfg = {"version": "v", "workloads": {f"q{i}": {"x": hashlib.sha256(str(i).encode()).hexdigest(), "i": i} for i in range(200)}}
print(json.dumps(derive.encode_chunks(cfg)))`
	out, err := exec.Command(py, "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	var feats map[string]string
	if err := jsonUnmarshal(out, &feats); err != nil {
		t.Fatal(err)
	}
	read := func(p string) ([]byte, error) {
		v, ok := feats["vm-config."+p[len("/vm-config/"):]]
		if !ok {
			return nil, fmt.Errorf("no %s", p)
		}
		return []byte(v), nil
	}
	raw, err := Config(read, "torii")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct{ Workloads map[string]any }
	if err := jsonUnmarshal(raw, &cfg); err != nil || len(cfg.Workloads) != 200 {
		t.Fatalf("%v %d", err, len(cfg.Workloads))
	}
	if len(feats) < 3 {
		t.Errorf("expected several parts, got %d features", len(feats))
	}
	// A part from another write: refused, not half-read.
	feats["vm-config.torii.1"] = feats["vm-config.torii.0"]
	if _, err := Config(read, "torii"); err == nil {
		t.Error("mixed parts accepted")
	}
	delete(feats, "vm-config.torii.0")
	if _, err := Config(read, "torii"); err == nil {
		t.Error("missing part accepted")
	}
	_ = os.Stdout
}
