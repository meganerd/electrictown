package pool

import "testing"

func TestDoomLoop_FirstResponseNotDoom(t *testing.T) {
	dl := NewDoomLoop()
	if dl.Check("hello world") {
		t.Error("first response should not be a doom loop")
	}
}

func TestDoomLoop_IdenticalResponseIsDoom(t *testing.T) {
	dl := NewDoomLoop()
	dl.Check("same output")
	if !dl.Check("same output") {
		t.Error("identical second response should be detected as doom loop")
	}
}

func TestDoomLoop_DifferentResponsesNotDoom(t *testing.T) {
	dl := NewDoomLoop()
	dl.Check("response A")
	if dl.Check("response B") {
		t.Error("different responses should not be a doom loop")
	}
}

func TestDoomLoop_ThirdIdenticalStillDoom(t *testing.T) {
	dl := NewDoomLoop()
	dl.Check("repeat")
	dl.Check("repeat")
	if !dl.Check("repeat") {
		t.Error("third identical response should still be detected")
	}
}

func TestDoomLoop_IndependentTrackers(t *testing.T) {
	dl1 := NewDoomLoop()
	dl2 := NewDoomLoop()
	dl1.Check("shared text")
	if dl2.Check("shared text") {
		t.Error("independent trackers should not share state")
	}
}

func TestDoomLoop_EmptyResponse(t *testing.T) {
	dl := NewDoomLoop()
	dl.Check("")
	if !dl.Check("") {
		t.Error("identical empty responses should be detected")
	}
}
