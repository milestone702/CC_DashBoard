// 审计日志：记录管理员写操作（谁在什么时候改了什么），便于多设备共用管理员 Key 时追溯。
package store

import "time"

// AuditLog 一条操作记录。
type AuditLog struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	Actor  string `json:"actor"`  // admin | admin(key)
	Method string `json:"method"` // POST/PUT/DELETE
	Path   string `json:"path"`
	Status int    `json:"status"`
	IP     string `json:"ip"`
}

// InsertAudit 写入一条操作记录。
func (db *DB) InsertAudit(actor, method, path string, status int, ip string) error {
	_, err := db.Exec(`INSERT INTO audit_logs(ts, actor, method, path, status, ip) VALUES(?,?,?,?,?,?)`,
		time.Now().Unix(), actor, method, path, status, ip)
	return err
}

// ListAudit 最近 N 条操作记录（新→旧）。
func (db *DB) ListAudit(limit int) ([]*AuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.Query(`SELECT id, ts, actor, method, path, status, ip
		FROM audit_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AuditLog
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.TS, &a.Actor, &a.Method, &a.Path, &a.Status, &a.IP); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}
