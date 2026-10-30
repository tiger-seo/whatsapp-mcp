package store

import "testing"

func TestFoldGolden(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"ПРИВІТ", "привіт"},
		{"ÉTÉ", "été"},
		{"été", "été"}, // decomposed becomes composed
		{"été", "été"},
		{"Straße", "strasse"},
		{"ΟΔΟΣ", "οδοσ"},
		{"50%_off", "50%_off"},
		{`a\b'c"`, `a\b'c"`},
	} {
		if got := fold(c.in); got != c.want {
			t.Errorf("fold(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if fold("ete") == fold("été") {
		t.Error("accents must still count: ete = été")
	}
}

func TestFoldSQLFunction(t *testing.T) {
	st, _ := openTest(t, newDataDir(t))
	var got string
	if err := st.reader.QueryRow("SELECT mcp_fold('ÉTÉ')").Scan(&got); err != nil || got != "été" {
		t.Errorf("reader mcp_fold = %q, %v", got, err)
	}
	if err := st.writer.QueryRow("SELECT mcp_fold('ПРИВІТ')").Scan(&got); err != nil || got != "привіт" {
		t.Errorf("writer mcp_fold = %q, %v", got, err)
	}
	var null *string
	if err := st.reader.QueryRow("SELECT mcp_fold(NULL)").Scan(&null); err != nil || null != nil {
		t.Errorf("mcp_fold(NULL) = %v, %v; want NULL", null, err)
	}
}
