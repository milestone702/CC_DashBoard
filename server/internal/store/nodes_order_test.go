package store

import (
	"testing"

	"cc_dashboard/server/internal/model"
)

// TestReorderNodes 验证创建顺序、ReorderNodes 调序与新节点排在末尾。
func TestReorderNodes(t *testing.T) {
	db := openTestDB(t)
	names := []string{"a", "b", "c"}
	ids := make([]int64, 0, len(names))
	for i, name := range names {
		id, err := db.CreateNode(&model.Node{Name: name, Token: "tok-" + name + string(rune('0'+i))})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		ids = append(ids, id)
	}

	list, err := db.ListNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Name != "a" || list[2].Name != "c" {
		t.Fatalf("默认顺序 = %v, want a,b,c", nodeNames(list))
	}

	// 调序：c,a,b
	if err := db.ReorderNodes([]int64{ids[2], ids[0], ids[1]}); err != nil {
		t.Fatal(err)
	}
	list, _ = db.ListNodes()
	want := []string{"c", "a", "b"}
	for i, n := range list {
		if n.Name != want[i] {
			t.Fatalf("调序后 = %v, want %v", nodeNames(list), want)
		}
	}

	// 新节点应排在已调序节点之后
	if _, err := db.CreateNode(&model.Node{Name: "d", Token: "tok-d"}); err != nil {
		t.Fatal(err)
	}
	list, _ = db.ListNodes()
	if list[len(list)-1].Name != "d" {
		t.Fatalf("新节点应在末尾, got %v", nodeNames(list))
	}
}

func nodeNames(nodes []*model.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Name)
	}
	return out
}
