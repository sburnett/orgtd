package org

import "testing"

func TestCloneHeadlineIsIndependent(t *testing.T) {
	f := mustParse(t)
	orig := f.Headlines[0] // "Someday goal", with two children

	clone := CloneHeadline(orig)

	if clone == orig {
		t.Fatalf("clone returned the same pointer")
	}
	if clone.Title != orig.Title || clone.Level != orig.Level {
		t.Errorf("clone = (title=%q level=%d), want (title=%q level=%d)", clone.Title, clone.Level, orig.Title, orig.Level)
	}
	if len(clone.Children) != len(orig.Children) {
		t.Fatalf("clone has %d children, want %d", len(clone.Children), len(orig.Children))
	}
	for i := range orig.Children {
		if clone.Children[i] == orig.Children[i] {
			t.Errorf("child %d shares a pointer with the original", i)
		}
		if clone.Children[i].Title != orig.Children[i].Title {
			t.Errorf("child %d title = %q, want %q", i, clone.Children[i].Title, orig.Children[i].Title)
		}
		if clone.Children[i].Parent != clone {
			t.Errorf("child %d Parent does not point at the cloned root", i)
		}
	}
	if clone.Parent != nil {
		t.Errorf("clone.Parent = %v, want nil", clone.Parent)
	}

	// Mutating the clone must not affect the original, and vice versa.
	clone.Title = "mutated"
	clone.Tags = append(clone.Tags, "extra")
	if orig.Title == "mutated" {
		t.Errorf("mutating the clone's title affected the original")
	}
	if len(orig.Tags) != 0 {
		t.Errorf("mutating the clone's tags affected the original: %v", orig.Tags)
	}

	task := f.Headlines[0].Children[0] // has properties and body
	taskClone := CloneHeadline(task)
	taskClone.Properties["ID"] = "changed"
	if task.Properties["ID"] == "changed" {
		t.Errorf("mutating the clone's properties affected the original")
	}
	taskClone.Body[0] = "changed"
	if task.Body[0] == "changed" {
		t.Errorf("mutating the clone's body affected the original")
	}
}
