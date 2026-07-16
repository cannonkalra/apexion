package csv

import "testing"

func TestSniffDelimiter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want rune
	}{
		{
			// eyeota IDFA feed: pipe separates the device id from a comma-list of
			// segments. Must sniff '|', not split the comma-list into 90+ columns.
			name: "pipe feed with commas inside a field",
			in:   "c5e26b9b-9513-4586-9976-6a83f3c8af80|1100,1066,1143\nabf1e2d3-1111-2222-3333-444455556666|1060,1061,1062\n7a8b9c0d-9999-8888-7777-666655554444|1042,1019,1015\n",
			want: '|',
		},
		{
			name: "plain comma csv stays comma",
			in:   "id,name,age\n1,alice,30\n2,bob,25\n3,carol,41\n",
			want: 0, // 0 → caller keeps the default comma
		},
		{
			name: "tab delimited",
			in:   "a\tb\tc\n1\t2\t3\n4\t5\t6\n",
			want: '\t',
		},
		{
			name: "semicolon delimited",
			in:   "a;b;c\n1;2;3\n4;5;6\n",
			want: ';',
		},
		{
			name: "comma csv with occasional pipe in data",
			in:   "a,b,c\nx|y,z,w\np,q,r\n",
			want: 0, // pipe count inconsistent → not chosen
		},
	}
	for _, c := range cases {
		if got := sniffDelimiter([]byte(c.in)); got != c.want {
			t.Errorf("%s: sniffDelimiter = %q, want %q", c.name, got, c.want)
		}
	}
}
