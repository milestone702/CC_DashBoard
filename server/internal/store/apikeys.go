// 机器凭证（API Key）存储：支持多条，供多设备/多脚本各自持有独立凭证。
// 只存 SHA-256，明文仅在生成时返回一次。
package store

import (
	"database/sql"
	"errors"
	"time"
)

// APIKey 一条机器凭证（Hash 不下发）。
type APIKey struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Scope     string `json:"scope"` // admin | view
	CreatedAt int64  `json:"created_at"`
	Hash      string `json:"-"`
}

// CreateAPIKey 新建凭证，返回记录（明文由调用方保留，不落库）。
func (db *DB) CreateAPIKey(name, scope, hash string) (*APIKey, error) {
	now := time.Now().Unix()
	res, err := db.Exec(`INSERT INTO api_keys(name, hash, scope, created_at) VALUES(?,?,?,?)`,
		name, hash, scope, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &APIKey{ID: id, Name: name, Scope: scope, CreatedAt: now}, nil
}

// ListAPIKeys 全部凭证（按创建时间升序）。
func (db *DB) ListAPIKeys() ([]*APIKey, error) {
	rows, err := db.Query(`SELECT id, name, scope, created_at FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Scope, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &k)
	}
	return out, rows.Err()
}

// FindAPIKeyByHash 按哈希查凭证（校验入口，等值查询命中唯一索引）。
func (db *DB) FindAPIKeyByHash(hash string) (*APIKey, error) {
	if hash == "" {
		return nil, ErrNotFound
	}
	var k APIKey
	err := db.QueryRow(`SELECT id, name, scope, created_at FROM api_keys WHERE hash=?`, hash).
		Scan(&k.ID, &k.Name, &k.Scope, &k.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k.Hash = hash
	return &k, nil
}

// FirstAPIKeyByName 取同名凭证中最早创建的一条（保留旧版「单 Key」接口语义）。
func (db *DB) FirstAPIKeyByName(name string) (*APIKey, error) {
	var k APIKey
	err := db.QueryRow(`SELECT id, name, scope, created_at FROM api_keys WHERE name=? ORDER BY id LIMIT 1`, name).
		Scan(&k.ID, &k.Name, &k.Scope, &k.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// DeleteAPIKey 按 id 吊销。
func (db *DB) DeleteAPIKey(id int64) (bool, error) {
	res, err := db.Exec(`DELETE FROM api_keys WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteAPIKeysByName 删除指定名字的全部凭证（旧版 rotate/delete 接口用）。
func (db *DB) DeleteAPIKeysByName(name string) (int64, error) {
	res, err := db.Exec(`DELETE FROM api_keys WHERE name=?`, name)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountAPIKeys 凭证总数（用于判断是否已配置）。
func (db *DB) CountAPIKeys() (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(1) FROM api_keys`).Scan(&n)
	return n, err
}
