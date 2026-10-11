// mkipk：构建 OpenWrt .ipk 安装包（ipkg 格式：gzip 压缩的 tar，成员 = ./debian-binary + ./control.tar.gz + ./data.tar.gz）。
// 注意：opkg (libopkg/deb_extract) 只认 gzip+tar，不支持 Debian 的 ar 归档，外层写 ar 会报 "Malformed package file"。
// 用法：go run . -bin ../../dist/cc-agent-linux-mipsle -arch mipsel_24kc -out ../../dist/agent.ipk
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"os"
	"time"
)

const controlTemplate = `Package: %s
Version: %s
Architecture: %s
Section: utils
Priority: optional
Maintainer: milestone702
License: MIT
Copyright: 2026 milestone702
Description: CC_DashBoard monitoring agent
`

// initdScript OpenWrt procd 模板（随 ipk 分发，安装即注册）。
// 配置缺失时跳过启动（logger 提示），写入 /etc/cc-agent/config.json 后 start 即可。
const initdScript = `#!/bin/sh /etc/rc.common
# USE_PROCD=1 必须有：缺失时 rc.common 的 start/stop 是空壳，restart 静默无效
USE_PROCD=1
START=99
STOP=10
PROG="/usr/bin/cc-agent"
CONFIG="/etc/cc-agent/config.json"

check_config() {
	[ -f "$CONFIG" ] || {
		logger -t cc-agent "缺少 $CONFIG（参考 /etc/cc-agent/config.example.json），跳过启动"
		return 1
	}
	return 0
}

start_service() {
	check_config || return 0
	procd_open_instance
	procd_set_param command $PROG --config $CONFIG
	procd_set_param respawn 3600 5 5
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_close_instance
}

reload_service() {
	stop
	start
}
`

// postinst 安装后注册开机自启并立即拉起（IPKG_INSTROOT 为固件离线安装时跳过）。
// 只 enable 不 start 会导致装完服务不跑、自动注册也不触发。
const postinstScript = `#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
[ -x /etc/init.d/cc-agent ] && /etc/init.d/cc-agent enable && /etc/init.d/cc-agent restart
exit 0
`

const prermScript = `#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
[ -x /etc/init.d/cc-agent ] && /etc/init.d/cc-agent disable
exit 0
`

// configExample 配置模板：token 留空 = 首次启动自动注册（后台批准准入）。
const configExample = `{
  "master": "http://192.168.1.100:8080",
  "token": "",
  "interval_sec": 5,
  "mode": "push",
  "listen": ":9100"
}
`

func main() {
	bin := flag.String("bin", "", "agent 二进制路径")
	arch := flag.String("arch", "", "OpenWrt 架构（如 mipsel_24kc / arm_cortex-a7）")
	version := flag.String("version", "0.1.0", "版本号")
	pkg := flag.String("pkg", "cc-agent", "包内可执行名")
	icon := flag.String("icon", "", "应用图标 PNG（可选，装到 /usr/share/pixmaps/<pkg>.png）")
	out := flag.String("out", "", "输出 .ipk 路径")
	flag.Parse()
	if *bin == "" || *arch == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := buildIPK(*bin, *arch, *version, *pkg, *icon, *out); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println("✓", *out)
}

func buildIPK(binPath, arch, version, pkg, iconPath, out string) error {
	bin, err := os.ReadFile(binPath)
	if err != nil {
		return err
	}
	// data：目录条目必须在前（opkg 解包不自动建父目录，靠 tar 里的目录项 mkdir）
	entries := []tarEntry{
		{name: "./usr/", mode: 0755, typeflag: tar.TypeDir},
		{name: "./usr/bin/", mode: 0755, typeflag: tar.TypeDir},
		{name: "./etc/", mode: 0755, typeflag: tar.TypeDir},
		{name: "./etc/init.d/", mode: 0755, typeflag: tar.TypeDir},
		{name: "./etc/" + pkg + "/", mode: 0755, typeflag: tar.TypeDir},
		{name: "./usr/bin/" + pkg, mode: 0755, data: bin},
		{name: "./etc/init.d/" + pkg, mode: 0755, data: []byte(initdScript)},
		{name: "./etc/" + pkg + "/config.example.json", mode: 0644, data: []byte(configExample)},
	}
	// 应用图标（可选）：目录项在前，文件在后
	if iconPath != "" {
		ic, err := os.ReadFile(iconPath)
		if err != nil {
			return fmt.Errorf("读取图标失败: %w", err)
		}
		entries = append(entries,
			tarEntry{name: "./usr/share/", mode: 0755, typeflag: tar.TypeDir},
			tarEntry{name: "./usr/share/pixmaps/", mode: 0755, typeflag: tar.TypeDir},
			tarEntry{name: "./usr/share/pixmaps/" + pkg + ".png", mode: 0644, data: ic},
		)
	}
	dataGz, err := tarGz(entries)
	if err != nil {
		return err
	}
	// control：control 描述 + postinst/prerm（安装即 enable 开机自启）
	control := fmt.Sprintf(controlTemplate, pkg, version, arch)
	controlGz, err := tarGz([]tarEntry{
		{name: "./control", mode: 0644, data: []byte(control)},
		{name: "./postinst", mode: 0755, data: []byte(postinstScript)},
		{name: "./prerm", mode: 0755, data: []byte(prermScript)},
	})
	if err != nil {
		return err
	}
	raw, err := tarGz([]tarEntry{
		{name: "./debian-binary", mode: 0644, data: []byte("2.0\n")},
		{name: "./control.tar.gz", mode: 0644, data: controlGz},
		{name: "./data.tar.gz", mode: 0644, data: dataGz},
	})
	if err != nil {
		return err
	}
	return os.WriteFile(out, raw, 0o644)
}

type tarEntry struct {
	name     string
	mode     int64
	typeflag byte // 0 = 普通文件；tar.TypeDir = 目录
	data     []byte
}

func tarGz(entries []tarEntry) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		h := &tar.Header{
			Name:     e.name,
			Mode:     e.mode,
			Size:     int64(len(e.data)),
			ModTime:  time.Now(),
			Typeflag: tf,
		}
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
		if _, err := tw.Write(e.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

