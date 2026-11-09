package langdet

import "testing"

func TestCheck(t *testing.T) {
	r := DefaultRules()
	cases := []struct {
		name string
		in   string
		want Verdict
	}{
		{"英文完整短句", "He go to school every day.", Correct},
		{"英文疑问句", "What are you doing?", Correct},
		{"英文感叹句", "This is amazing!", Correct},
		{"英文未完成句", "He go to school", Pending},
		{"中文句", "他每天去学校。", Reject},
		{"中文未完成", "他每天去学校", Reject},
		{"中英混排", "我觉得 the weather is nice today.", Reject},
		{"长句跳过", longEnglish(), Reject},
		{"纯数字", "12345 67890", Reject},
		{"单字跳过", "Hello.", Reject},
		{"空串", "", Reject},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Check(c.in, r); got != c.want {
				t.Fatalf("Check(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestExtractSentence(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"I like apples. I like oranges", "I like oranges"},
		{"第一句。第二句", "第二句"},
		{"just typed the dot.", "just typed the dot."},
		{"", ""},
	}
	for _, c := range cases {
		if got := ExtractSentence(c.in); got != c.want {
			t.Fatalf("ExtractSentence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHasCJK(t *testing.T) {
	if !HasCJK("你好") || !HasCJK("日本語") || HasCJK("hello world") {
		t.Fatal("HasCJK misjudged")
	}
}

// longEnglish 生成 50 词的英文长句（超过 MaxWords=40）。
func longEnglish() string {
	words := ""
	for i := 0; i < 50; i++ {
		words += " word"
	}
	return words[1:] + "."
}
