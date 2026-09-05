package engine

import "testing"

func TestRunsAllowsOneAtATime(t *testing.T) {
	r := NewRuns()
	if !r.Begin("s1", func() {}) {
		t.Fatal("最初の占有が取れない")
	}
	// 2 つ目の入口 (Discord) から同じ会話を始めようとしても通さない。
	// 通すと、履歴の書き込み先が衝突する。
	if r.Begin("s1", func() {}) {
		t.Fatal("同じ会話で二重に始められた")
	}
	if !r.Begin("s2", func() {}) {
		t.Fatal("別の会話まで止めている")
	}

	if !r.Running("s1") {
		t.Fatal("走っていることになっていない")
	}
	r.End("s1")
	if r.Running("s1") {
		t.Fatal("終わっても占有が残っている")
	}
	if !r.Begin("s1", func() {}) {
		t.Fatal("終わった後に始められない")
	}
}

func TestRunsCancelCallsAndKeepsHold(t *testing.T) {
	r := NewRuns()
	stopped := false
	r.Begin("s1", func() { stopped = true })

	if !r.Cancel("s1") {
		t.Fatal("走っているのに中断できない")
	}
	if !stopped {
		t.Fatal("中断が呼ばれていない")
	}
	// 占有は解かない。解くのは走っている側で、ここで消すと後から始まった
	// ターンの中断関数を取り違える。
	if !r.Running("s1") {
		t.Fatal("中断で占有まで解けている")
	}
	if r.Cancel("s2") {
		t.Fatal("走っていない会話を中断したことになっている")
	}
}

func TestRunsEndReleasesContext(t *testing.T) {
	r := NewRuns()
	stopped := false
	r.Begin("s1", func() { stopped = true })
	r.End("s1")
	// 途中で抜けた場合に文脈が漏れ続けないよう、終了でも中断を呼ぶ。
	if !stopped {
		t.Fatal("終了で中断関数が呼ばれていない")
	}
}
