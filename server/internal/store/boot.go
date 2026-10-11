// 开关机记录读写（插入去重：主键 (node_id, ts, type)）。
package store

import (
	"cc_dashboard/server/internal/model"
)

// InsertBootEvents 批量写入开关机记录，已存在的（同节点同时间同类型）忽略。
func (db *DB) InsertBootEvents(nodeID int64, evs []model.BootEvent) error {
	if len(evs) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO boot_events(node_id, ts, type) VALUES (?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range evs {
		if e.TS <= 0 || (e.Type != "on" && e.Type != "off") {
			continue
		}
		if _, err := stmt.Exec(nodeID, e.TS, e.Type); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListBootEvents 读取节点开关机记录（新→旧）。
func (db *DB) ListBootEvents(nodeID int64, limit int) ([]model.BootEvent, error) {
	rows, err := db.Query(`SELECT ts, type FROM boot_events WHERE node_id=? ORDER BY ts DESC LIMIT ?`,
		nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.BootEvent{}
	for rows.Next() {
		var e model.BootEvent
		if err := rows.Scan(&e.TS, &e.Type); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
