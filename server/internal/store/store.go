// SQLite 持久化：连接、迁移与公共辅助。
package store

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，免 CGO
)

// DB 封装 SQLite 连接。
type DB struct {
	*sql.DB
	dir string // 数据库所在目录（程序池等附属文件的根）
}

// DataDir 返回数据库所在目录。
func (db *DB) DataDir() string { return db.dir }

// Open 打开数据库、启用 WAL 并执行建表迁移。
func Open(path string) (*DB, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// WAL 模式允许并发读；写仍串行（busy_timeout 5s 兜底重试），
	// 多连接避免「采集+API+降采样」互相排队。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &DB{db, dir}, nil
}
