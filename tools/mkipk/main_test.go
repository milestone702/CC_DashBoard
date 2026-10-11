package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildIPK(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "agent")
	payload := []byte("#!/bin/sh\necho cc-agent\n")
	if err := os.WriteFile(binPath, payload, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "cc-agent.ipk")
	if err := buildIPK(binPath, "mipsel_24kc", "0.1.0", "cc-agent", "", out); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// ipkg 格式：外层是 gzip 压缩的 tar（opkg 不支持 Debian ar 归档）
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		t.Fatalf("外层不是 gzip：magic = % x", raw[:min(2, len(raw))])
	}

	members := untarGz(t, raw)
	if string(members["./debian-binary"]) != "2.0\n" {
		t.Errorf("debian-binary = %q", members["./debian-binary"])
	}
	if _, ok := members["./control.tar.gz"]; !ok {
		t.Error("缺少 ./control.tar.gz")
	}
	if _, ok := members["./data.tar.gz"]; !ok {
		t.Error("缺少 ./data.tar.gz")
	}

	// data.tar.gz 内容校验
	entries := untarGz(t, members["./data.tar.gz"])
	// opkg 解包不自动创建父目录：目录条目必须存在，且排在子文件之前
	dataOrder := tarEntriesInOrder(t, members["./data.tar.gz"])
	pos := map[string]int{}
	for i, e := range dataOrder {
		pos[e.name] = i
	}
	for i, e := range dataOrder {
		parent := parentDir(e.name)
		if parent == "./" {
			continue
		}
		pi, ok := pos[parent]
		if !ok {
			t.Errorf("data 缺少目录条目 %s（%s 的父目录）", parent, e.name)
		} else if pi > i {
			t.Errorf("目录 %s 必须排在 %s 之前", parent, e.name)
		}
	}
	got, ok := entries["./usr/bin/cc-agent"]
	if !ok {
		t.Fatalf("data entries = %v", keys(entries))
	}
	if !bytes.Equal(got, payload) {
		t.Error("二进制内容不一致")
	}
	// control 校验
	ctrl := untarGz(t, members["./control.tar.gz"])
	body, ok := ctrl["./control"]
	if !ok || !strings.Contains(string(body), "Architecture: mipsel_24kc") || !strings.Contains(string(body), "Package: cc-agent") {
		t.Errorf("control = %q", body)
	}
}

func untarGz(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = b
	}
	return out
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

type tarEnt struct {
	name  string
	isDir bool
}

// tarEntriesInOrder 按写入顺序列出成员（map 会丢顺序，目录先于文件的约束需要顺序）
func tarEntriesInOrder(t *testing.T, data []byte) []tarEnt {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var out []tarEnt
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, tarEnt{name: h.Name, isDir: h.Typeflag == tar.TypeDir})
		if _, err := io.Copy(io.Discard, tr); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// parentDir 返回 "./a/b/c" 的父目录 "./a/b/"；根下一级返回 "./"
func parentDir(name string) string {
	p := strings.TrimPrefix(name, "./")
	p = strings.TrimSuffix(p, "/")
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "./"
	}
	return "./" + p[:i+1]
}
