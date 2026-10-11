// settings 表读写（密码哈希、视图密码开关、join key 等）。
package store

import (
	"database/sql"
	"errors"
)

// GetSetting 读取配置项；不存在返回 ("", false, nil)。
func (db *DB) GetSetting(key string) (string, bool, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetSetting 写入/更新配置项。
func (db *DB) SetSetting(key, value string) error {
	_, err := db.Exec(`INSERT INTO settings(key, value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// DeleteSetting 删除配置项。
func (db *DB) DeleteSetting(key string) error {
	_, err := db.Exec(`DELETE FROM settings WHERE key=?`, key)
	return err
}
