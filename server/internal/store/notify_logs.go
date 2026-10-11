// 通知发送记录（成功/失败落库，后台可查）。
package store

import "time"

// NotifyLog 单条发送记录。
type NotifyLog struct {
	ID      int64  `json:"id"`
	TS      int64  `json:"ts"`
	Channel string `json:"channel"`
	Title   string `json:"title"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// InsertNotifyLog 写入发送记录（error 超长截断到 500 字符）。
func (db *DB) InsertNotifyLog(channel, title string, ok bool, errMsg string) error {
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	_, err := db.Exec(`INSERT INTO notify_logs(ts, channel, title, ok, error) VALUES(?,?,?,?,?)`,
		time.Now().Unix(), channel, title, boolInt(ok), errMsg)
	return err
}

// ListNotifyLogs 最近记录（按时间倒序）。
func (db *DB) ListNotifyLogs(limit int) ([]*NotifyLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Query(`SELECT id, ts, channel, title, ok, error FROM notify_logs
		ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NotifyLog
	for rows.Next() {
		var l NotifyLog
		var ok int
		if err := rows.Scan(&l.ID, &l.TS, &l.Channel, &l.Title, &ok, &l.Error); err != nil {
			return nil, err
		}
		l.OK = ok != 0
		out = append(out, &l)
	}
	return out, rows.Err()
}

// CleanupNotifyLogs 保留最近 30 天记录。
func (db *DB) CleanupNotifyLogs(now int64) error {
	_, err := db.Exec(`DELETE FROM notify_logs WHERE ts < ?`, now-30*24*3600)
	return err
}
