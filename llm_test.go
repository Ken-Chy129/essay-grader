package main

import "testing"

func TestCleanTranscript(t *testing.T) {
	cases := []struct{ in, want string }{
		// 剥掉开头的“前言”行
		{"图片是倒置的,我已按正常方向理解并转写如下:\n以青春之笔,谱写人生道路。\n看着手机……",
			"以青春之笔,谱写人生道路。\n看着手机……"},
		// 剥掉结尾的“建议”备注
		{"以青春之笔,谱写人生道路。\n由于字迹潦草,部分内容可能存在错误,建议提供更清晰的图片。",
			"以青春之笔,谱写人生道路。"},
		// 正常正文不动
		{"以青春之笔,谱写人生道路。\n人生有失有得。",
			"以青春之笔,谱写人生道路。\n人生有失有得。"},
		// 正文里含冒号但非元说明,保留
		{"他说:学妹,要珍惜。\n这便是青春。", "他说:学妹,要珍惜。\n这便是青春。"},
	}
	for i, c := range cases {
		if got := cleanTranscript(c.in); got != c.want {
			t.Errorf("case %d:\n got=%q\nwant=%q", i, got, c.want)
		}
	}
}

func TestExtractJSON(t *testing.T) {
	// JSON 后面跟解释文字时,只取第一个完整对象
	in := `{"total":30,"overall":"还行{注}"} 以上为评分,仅供参考æ`
	want := `{"total":30,"overall":"还行{注}"}`
	if got := extractJSON(in); got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}
