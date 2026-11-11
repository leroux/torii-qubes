package config

import "testing"

func TestExampleFileParses(t *testing.T) {
	if _, err := Load("../../example-config.json"); err != nil {
		t.Fatal(err)
	}
}
