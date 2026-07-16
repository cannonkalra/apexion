package sqlgen

import (
	"strings"
	"testing"

	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/selection/compat"
)

// fakeReader mimics duckdb.ListReader without CGO: read_<fmt>([uris]).
func fakeReader(uris []string, format model.Format, _ model.ReadOptions) (string, error) {
	q := make([]string, len(uris))
	for i, u := range uris {
		q[i] = "'" + u + "'"
	}
	return "read_" + string(format) + "([" + strings.Join(q, ", ") + "])", nil
}

func ref(key string) compat.FileRef {
	return compat.FileRef{Bucket: "warehouse", Key: key, Format: model.FormatParquet}
}

func TestBuild(t *testing.T) {
	g := compat.Grouping{
		Members: []compat.FileRef{ref("data/orders/o1.parquet"), ref("data/orders/o2.parquet")},
		Merged:  []compat.MergedColumn{{Name: "id"}, {Name: "price"}},
	}
	gt, err := Build(g, model.FormatParquet, model.DefaultReadOptions(), fakeReader)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := "SELECT * FROM read_parquet(['s3://warehouse/data/orders/o1.parquet', 's3://warehouse/data/orders/o2.parquet'])"
	if gt.SQL != want {
		t.Errorf("SQL =\n%q\nwant\n%q", gt.SQL, want)
	}
	if gt.Name != "orders" {
		t.Errorf("Name = %q, want orders", gt.Name)
	}
}

func TestBuildEmpty(t *testing.T) {
	gt, err := Build(compat.Grouping{}, model.FormatCSV, model.DefaultReadOptions(), fakeReader)
	if err != nil {
		t.Fatalf("Build empty: %v", err)
	}
	if gt.SQL != "" {
		t.Errorf("empty grouping SQL = %q, want empty", gt.SQL)
	}
}

func TestTableName(t *testing.T) {
	cases := []struct {
		keys []string
		want string
	}{
		{[]string{"data/orders/a.parquet", "data/orders/b.parquet"}, "orders"},
		{[]string{"a.parquet", "b.parquet"}, "a"},                         // no shared dir → first stem
		{[]string{"2023-01/x.parquet", "2023-01/y.parquet"}, "t_2023_01"}, // leading digit fixed
	}
	for _, c := range cases {
		members := make([]compat.FileRef, len(c.keys))
		for i, k := range c.keys {
			members[i] = ref(k)
		}
		if got := TableName(members, model.FormatParquet); got != c.want {
			t.Errorf("TableName(%v) = %q, want %q", c.keys, got, c.want)
		}
	}
}
