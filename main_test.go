package main

import "testing"

func TestDirectoryExclusion(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"etc/myconfig.txt", true},
		{"opt/myconfig", false},
		{"var", true},
		{"run/foo/bar/baz", true},
	}

	for _, test := range tests {
		actual := DirectoryIsExcluded(test.input)
		if actual != test.expected {
			t.Fatalf("want=%t, got=%t for input %s", test.expected, actual, test.input)
			t.Fail()
		}
	}
}

func TestFileExclusion(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"etc/myconfig.txt", false},
		{"etc/resolv.conf", true},
	}

	for _, test := range tests {
		actual := PathIsExcluded(test.input)
		if actual != test.expected {
			t.Fatalf("want=%t, got=%t for input %s", test.expected, actual, test.input)
			t.Fail()
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/my/path", "my/path"},
		{"./this/that", "this/that"},
		{"this/that/../foo", "this/foo"},
		{"this/../that", "that"},
		{"/", "/"},
	}

	for _, test := range tests {
		actual := Normalize(test.input)
		if actual != test.expected {
			t.Fatalf(`want="%s", got="%s" for input "%s"`, test.expected, actual, test.input)
			t.Fail()
		}
	}
}
