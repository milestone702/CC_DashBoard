// CC_DashBoard Master 主控服务入口。
package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"cc_dashboard/server/internal/alert"
	"cc_dashboard/server/internal/api"
	"cc_dashboard/server/internal/collector"
	"cc_dashboard/server/internal/security"
	"cc_dashboard/server/internal/store"
)

// version 构建时注入：go build -ldflags "-X main.version=$(git describe --tags)"
var version = "dev"

func main() {
	addr := flag.String("addr", ":8080", "HTTP 监听地址")
	dataDir := flag.String("data", "./data", "数据目录（SQLite + 主密钥）")
	webDir := flag.String("web", "", "前端构建产物目录（如 ../web/dist），非空则单端口托管")
	install := flag.Bool("install", false, "交互式安装：注册系统服务（sc/systemd/procd）")
	showVer := flag.Bool("version", false, "打印版本号后退出")
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}

	if *install {
		runServerInstall()
		return
	}

	// 1. 主密钥：AES-256-GCM，用于加密 SSH 密码 / API Token
	key, err := security.LoadOrCreateKey(filepath.Join(*dataDir, "master.key"))
	if err != nil {
		log.Fatalf("主密钥初始化失败: %v", err)
	}

	// 2. SQLite（WAL）+ 建表迁移
	db, err := store.Open(filepath.Join(*dataDir, "cc_dashboard.db"))
	if err != nil {
		log.Fatalf("数据库打开失败: %v", err)
	}
	defer db.Close()

	// 3. 告警引擎（离线巡检协程）与 WebSocket 集线器
	engine := alert.NewEngine(db)
	hub := api.NewHub()
	engine.Broadcast = func(v any) { hub.Broadcast(v) } // 上下线实时推送
	go engine.Start()
	go hub.Run()

	// 3.5 采集引擎：SSH 轮询（模式 B）+ 正向拉取 + PVE/群晖 API（模式 C）
	mgr := collector.NewManager(db, key, engine, func(v any) { hub.Broadcast(v) })
	mgr.Start()

	// 4. 降采样：5s→1m、1m→1h（周期补齐最近窗口，幂等）
	go rollupLoop(db)

	// 4.5 版本注入（程序池管理页与池中 server 版本对比提示）
	api.Version = version

	// 5. 过期清理：启动时 + 每 6 小时
	go cleanupLoop(db)

	// 6. HTTP + WebSocket（live 缓存回调注入采集引擎）
	log.Printf("CC_DashBoard Master %s 启动于 %s（数据目录 %s）", version, *addr, *dataDir)
	router, apiSrv := api.NewRouter(db, key, hub, engine, *webDir)
	mgr.OnLive = apiSrv.SetLive
	if err := router.Run(*addr); err != nil {
		log.Fatal(err)
	}
}

func rollupLoop(db *store.DB) {
	for range time.NewTicker(30 * time.Second).C {
		now := time.Now().Unix()
		if err := db.AggregateUp(store.Table5s, store.Table1m, 60, now-660, now); err != nil {
			log.Printf("降采样 5s→1m 失败: %v", err)
		}
		if err := db.AggregateUp(store.Table1m, store.Table1h, 3600, now-3*3600, now); err != nil {
			log.Printf("降采样 1m→1h 失败: %v", err)
		}
	}
}

func cleanupLoop(db *store.DB) {
	run := func() {
		if err := db.Cleanup(time.Now().Unix()); err != nil {
			log.Printf("历史清理失败: %v", err)
		}
		if err := db.CleanupNotifyLogs(time.Now().Unix()); err != nil {
			log.Printf("通知记录清理失败: %v", err)
		}
	}
	run()
	for range time.NewTicker(6 * time.Hour).C {
		run()
	}
}
