package api

import "testing"

func TestLocalizeDatabaseError(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "删除节点失败：constraint failed: FOREIGN KEY constraint failed (1811)",
			want:  "删除节点失败：仍有其他数据引用该对象，请先解除关联",
		},
		{
			input: "创建节点失败：constraint failed: UNIQUE constraint failed: nodes.name (2067)",
			want:  "创建节点失败：数据已存在，不能重复",
		},
		{
			input: "普通中文错误",
			want:  "普通中文错误",
		},
	}
	for _, test := range tests {
		if got := localizeDatabaseError(test.input); got != test.want {
			t.Fatalf("localizeDatabaseError(%q) = %q，预期 %q", test.input, got, test.want)
		}
	}
}
