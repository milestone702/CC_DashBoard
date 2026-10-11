// 程序池（Agent / Dashboard 安装包元数据）读写。
package store

import (
	"database/sql"
	"errors"
	"time"

	"cc_dashboard/server/internal/model"
)

// InsertPoolItem 写入池条目，返回自增 ID。
func (db *DB) InsertPoolItem(it *model.PoolItem) (int64, error) {
	if it.CreatedAt == 0 {
		it.CreatedAt = time.Now().Unix()
	}
	res, err := db.Exec(`INSERT INTO pool_items
		(component, version, goos, arch, format, filename, sha256, size, note, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		it.Component, it.Version, it.GOOS, it.Arch, it.Format,
		it.Filename, it.SHA256, it.Size, it.Note, it.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const poolCols = `id, component, version, goos, arch, format, filename, sha256, size, note, created_at`

func scanPool(row interface{ Scan(...any) error }) (*model.PoolItem, error) {
	var it model.PoolItem
	err := row.Scan(&it.ID, &it.Component, &it.Version, &it.GOOS, &it.Arch, &it.Format,
		&it.Filename, &it.SHA256, &it.Size, &it.Note, &it.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &it, nil
}

// ListPoolItems 按组件列出池条目（新→旧）；component 为空列出全部。
func (db *DB) ListPoolItems(component string) ([]*model.PoolItem, error) {
	q := `SELECT ` + poolCols + ` FROM pool_items`
	args := []any{}
	if component != "" {
		q += ` WHERE component=?`
		args = append(args, component)
	}
	q += ` ORDER BY created_at DESC, id DESC`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.PoolItem
	for rows.Next() {
		it, err := scanPool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetPoolItem 按 ID 查询。
func (db *DB) GetPoolItem(id int64) (*model.PoolItem, error) {
	return scanPool(db.QueryRow(`SELECT `+poolCols+` FROM pool_items WHERE id=?`, id))
}

// DeletePoolItem 删除池条目（文件由调用方一并清理）。
func (db *DB) DeletePoolItem(id int64) error {
	res, err := db.Exec(`DELETE FROM pool_items WHERE id=?`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	return nil
}

// PoolLatest 取指定匹配维度下最新上传的池条目；format 为空表示任意格式。
// 版本比较不做排序，仅以创建时间最新的条目为“最新”。
func (db *DB) PoolLatest(component, goos, arch, format string) (*model.PoolItem, error) {
	q := `SELECT ` + poolCols + ` FROM pool_items
		WHERE component=? AND goos=? AND arch=?`
	args := []any{component, goos, arch}
	if format != "" {
		q += ` AND format=?`
		args = append(args, format)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT 1`
	return scanPool(db.QueryRow(q, args...))
}
