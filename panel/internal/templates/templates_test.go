package templates

import "testing"

func TestListReturnsSeven(t *testing.T) {
	list := List()
	if len(list) != 7 {
		t.Fatalf("List() len = %d, want 7", len(list))
	}
	// Ensure copy is independent
	list[0].Protocol = "mutated"
	if List()[0].Protocol == "mutated" {
		t.Fatal("List should return a copy")
	}
}

func TestGetEachProtocol(t *testing.T) {
	want := map[string]string{
		"shadowsocks": "inbound.shadowsocks.v1",
		"trojan":      "inbound.trojan.v1",
		"vless":       "inbound.vless.v1",
		"hysteria2":   "inbound.hysteria2.v1",
		"tuic":        "inbound.tuic.v1",
		"anytls":      "inbound.anytls.v1",
		"vmess":       "inbound.vmess.v1",
	}
	for proto, id := range want {
		tpl, ok := Get(proto)
		if !ok {
			t.Fatalf("Get(%q) not found", proto)
		}
		if tpl.Protocol != proto {
			t.Fatalf("Get(%q).Protocol = %q", proto, tpl.Protocol)
		}
		if tpl.ID != id {
			t.Fatalf("Get(%q).ID = %q, want %q", proto, tpl.ID, id)
		}
		if len(tpl.Fields) == 0 {
			t.Fatalf("Get(%q) has no fields", proto)
		}
	}
	if _, ok := Get("unknown"); ok {
		t.Fatal("Get(unknown) should be false")
	}
}

func TestShadowsocksMethodSelect(t *testing.T) {
	tpl, ok := Get("shadowsocks")
	if !ok {
		t.Fatal("shadowsocks template missing")
	}
	var method Field
	found := false
	for _, f := range tpl.Fields {
		if f.Name == "method" {
			method = f
			found = true
			break
		}
	}
	if !found {
		t.Fatal("method field missing")
	}
	if method.Type != "select" {
		t.Fatalf("method type = %q, want select", method.Type)
	}
	if len(method.Options) < 4 {
		t.Fatalf("expected AEAD method options, got %v", method.Options)
	}
}

func TestVLESSTLSMode(t *testing.T) {
	tpl, ok := Get("vless")
	if !ok {
		t.Fatal("vless template missing")
	}
	var mode Field
	for _, f := range tpl.Fields {
		if f.Name == "tls_mode" {
			mode = f
			break
		}
	}
	if mode.Type != "select" {
		t.Fatalf("tls_mode type = %q", mode.Type)
	}
	want := map[string]bool{"none": true, "tls": true, "reality": true}
	for _, o := range mode.Options {
		delete(want, o)
	}
	if len(want) != 0 {
		t.Fatalf("missing tls_mode options: %v", want)
	}
}
