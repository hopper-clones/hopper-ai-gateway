package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseConfigBytesClientHosts(t *testing.T) {
	cfg, errParse := ParseConfigBytes([]byte("client-hosts:\n  - 192.0.2.10\n  - 2001:db8::10\n"))
	if errParse != nil {
		t.Fatalf("ParseConfigBytes() error = %v", errParse)
	}
	if want := []string{"192.0.2.10", "2001:db8::10"}; !reflect.DeepEqual(cfg.ClientHosts, want) {
		t.Fatalf("ClientHosts = %v, want %v", cfg.ClientHosts, want)
	}
}

func TestParseConfigBytesClientHostsV8Layout(t *testing.T) {
	cfg, errParse := ParseConfigBytes([]byte("config-version: 8\nserver:\n  host: 127.0.0.1\n  port: 8317\n  client-hosts:\n    - 192.0.2.10\n"))
	if errParse != nil {
		t.Fatalf("ParseConfigBytes() error = %v", errParse)
	}
	if want := []string{"192.0.2.10"}; !reflect.DeepEqual(cfg.ClientHosts, want) || cfg.Host != "127.0.0.1" {
		t.Fatalf("ClientHosts = %v host = %q, want %v on 127.0.0.1", cfg.ClientHosts, cfg.Host, want)
	}
}

func TestValidateClientHostsRefusesWildcardsAndNames(t *testing.T) {
	for _, hosts := range [][]string{{"0.0.0.0"}, {"::"}, {"localhost"}, {"example.test"}, {""}, {" 192.0.2.10"}, {"192.0.2.0/24"}, {"192.0.2.10", "192.0.2.10"}} {
		if errValidate := ValidateClientHosts(hosts); errValidate == nil {
			t.Fatalf("ValidateClientHosts(%q) = nil, want an error", hosts)
		}
	}
	if errValidate := ValidateClientHosts([]string{"192.0.2.10", "127.0.0.2", "2001:db8::10"}); errValidate != nil {
		t.Fatalf("ValidateClientHosts() error = %v", errValidate)
	}
}

func TestSaveConfigKeepsClientHostsInTheServerBlock(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	raw := "config-version: 8\nserver:\n  host: 127.0.0.1\n  port: 8317\n  client-hosts:\n    - 192.0.2.10\n"
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	cfg.APIKeys = []string{"client-key"}
	if err = SaveConfigPreserveComments(file, cfg, false); err != nil {
		t.Fatal(err)
	}
	again, err := LoadConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"192.0.2.10"}; !reflect.DeepEqual(again.ClientHosts, want) || again.Host != "127.0.0.1" {
		t.Fatalf("after save ClientHosts = %v host = %q, want %v on 127.0.0.1", again.ClientHosts, again.Host, want)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\nclient-hosts:") {
		t.Fatalf("client-hosts saved at the top level:\n%s", data)
	}
}
