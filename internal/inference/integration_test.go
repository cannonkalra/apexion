package inference

import (
	"bytes"
	"context"
	"io"
	"testing"

	pq "github.com/parquet-go/parquet-go"

	csvreader "github.com/apexion/apexion/internal/crawler/csv"
	"github.com/apexion/apexion/internal/crawler/format"
	jsonreader "github.com/apexion/apexion/internal/crawler/json"
	parquetreader "github.com/apexion/apexion/internal/crawler/parquet"
	"github.com/apexion/apexion/internal/model"
)

// memSource is an in-memory format.Source for tests.
type memSource struct {
	key  string
	data []byte
}

func (m memSource) Key() string { return m.key }
func (m memSource) Size() int64 { return int64(len(m.data)) }
func (m memSource) Open(ctx context.Context) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.data)), nil
}
func (m memSource) ReaderAt(ctx context.Context) (io.ReaderAt, int64, error) {
	return bytes.NewReader(m.data), int64(len(m.data)), nil
}

func fieldByName(fields []format.Field, name string) *format.Field {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i]
		}
	}
	return nil
}

func TestCSVReaderAndInference(t *testing.T) {
	csvData := `id,email,amount,country,created
1,alice@example.com,10.50,US,2023-01-01
2,bob@example.com,22.00,GB,2023-01-02
3,carol@test.org,5.25,DE,2023-01-03
4,dan@example.com,100.00,US,2023-01-04`

	src := memSource{key: "orders.csv", data: []byte(csvData)}
	res, err := csvreader.NewCSV().ReadSchema(context.Background(), src, format.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fields) != 5 {
		t.Fatalf("want 5 fields, got %d: %+v", len(res.Fields), res.Fields)
	}
	if f := fieldByName(res.Fields, "id"); f == nil || f.Type != model.TypeInteger {
		t.Fatalf("id should be integer: %+v", f)
	}
	if f := fieldByName(res.Fields, "amount"); f == nil || (f.Type != model.TypeFloat && f.Type != model.TypeDecimal) {
		t.Fatalf("amount should be float/decimal: %+v", f)
	}
	if f := fieldByName(res.Fields, "created"); f == nil || f.Type != model.TypeDate {
		t.Fatalf("created should be date: %+v", f)
	}

	ir := NewEngine().Analyze(Input{
		DatasetID: "d1", Fields: res.Fields, Rows: res.Rows, DetectPII: true, MaxSampleValues: 5,
	})
	email := findCol(ir.Columns, "email")
	if email == nil || email.SemanticType != model.SemanticEmail || !email.IsPII {
		t.Fatalf("email semantic/pii wrong: %+v", email)
	}
	country := findCol(ir.Columns, "country")
	if country == nil || country.SemanticType != model.SemanticCountry {
		t.Fatalf("country semantic wrong: %+v", country)
	}
	if len(ir.PrimaryKeys) == 0 || ir.PrimaryKeys[0] != "id" {
		t.Fatalf("id should be PK candidate: %+v", ir.PrimaryKeys)
	}
	if len(ir.PIIColumns) == 0 {
		t.Fatal("expected PII columns")
	}
	if ir.QualityScore <= 0 || ir.QualityScore > 100 {
		t.Fatalf("bad quality score: %v", ir.QualityScore)
	}
}

func TestJSONLReader(t *testing.T) {
	data := `{"id":1,"name":"Alice","tags":["a","b"],"addr":{"city":"NYC"}}
{"id":2,"name":"Bob","tags":["c"],"addr":{"city":"LA"}}`
	src := memSource{key: "people.jsonl", data: []byte(data)}
	res, err := jsonreader.NewJSONL().ReadSchema(context.Background(), src, format.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if f := fieldByName(res.Fields, "tags"); f == nil || f.Type != model.TypeArray {
		t.Fatalf("tags should be array: %+v", f)
	}
	if f := fieldByName(res.Fields, "addr"); f == nil || f.Type != model.TypeStruct {
		t.Fatalf("addr should be struct: %+v", f)
	}
	if f := fieldByName(res.Fields, "id"); f == nil || f.Type != model.TypeInteger {
		t.Fatalf("id should be integer: %+v", f)
	}
}

type pqRow struct {
	ID    int64   `parquet:"id"`
	Name  string  `parquet:"name"`
	Price float64 `parquet:"price"`
}

func TestParquetReader(t *testing.T) {
	var buf bytes.Buffer
	w := pq.NewGenericWriter[pqRow](&buf)
	if _, err := w.Write([]pqRow{
		{1, "widget", 9.99}, {2, "gadget", 19.95}, {3, "gizmo", 4.50},
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	src := memSource{key: "products.parquet", data: buf.Bytes()}
	res, err := parquetreader.New().ReadSchema(context.Background(), src, format.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fields) != 3 {
		t.Fatalf("want 3 fields: %+v", res.Fields)
	}
	if res.RowCountEstimate != 3 {
		t.Fatalf("want 3 rows, got %d", res.RowCountEstimate)
	}
	if f := fieldByName(res.Fields, "id"); f == nil || f.Type != model.TypeInteger {
		t.Fatalf("id should be integer: %+v", f)
	}
	if f := fieldByName(res.Fields, "price"); f == nil || f.Type != model.TypeFloat {
		t.Fatalf("price should be float: %+v", f)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("want 3 sample rows, got %d", len(res.Rows))
	}
}

func TestDetectFormat(t *testing.T) {
	cases := []struct {
		key    string
		header []byte
		want   model.Format
	}{
		{"a/b/data.csv", nil, model.FormatCSV},
		{"a/b/data.tsv", nil, model.FormatTSV},
		{"a/b/data.jsonl", nil, model.FormatJSONL},
		{"a/b/data.parquet", nil, model.FormatParquet},
		{"noext", []byte("PAR1abcd"), model.FormatParquet},
		{"noext", []byte("Obj\x01xx"), model.FormatAvro},
		{"noext", []byte("ORCxx"), model.FormatORC},
	}
	for _, c := range cases {
		if got := format.DetectFormat(c.key, c.header); got != c.want {
			t.Errorf("DetectFormat(%q)=%s want %s", c.key, got, c.want)
		}
	}
}

func findCol(cols []model.ColumnInference, name string) *model.ColumnInference {
	for i := range cols {
		if cols[i].Name == name {
			return &cols[i]
		}
	}
	return nil
}
