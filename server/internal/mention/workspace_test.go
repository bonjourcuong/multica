package mention

import (
	"reflect"
	"testing"
)

func TestParseWorkspaceMentions(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []WorkspaceMention
	}{
		{
			name: "single slug",
			in:   "hello @fuchsia-b2b please push",
			want: []WorkspaceMention{{WorkspaceSlug: "fuchsia-b2b"}},
		},
		{
			name: "slug:agent",
			in:   "@fuchsia-b2b:Tony do X",
			want: []WorkspaceMention{{WorkspaceSlug: "fuchsia-b2b", AgentName: "Tony"}},
		},
		{
			name: "two mentions",
			in:   "@aa and @bb",
			want: []WorkspaceMention{{WorkspaceSlug: "aa"}, {WorkspaceSlug: "bb"}},
		},
		{
			name: "no mention",
			in:   "no mention here",
			want: nil,
		},
		{
			name: "email is not a mention",
			in:   "email me at foo@bar.com",
			want: nil,
		},
		{
			name: "rejects leading dash",
			in:   "@-bad",
			want: nil,
		},
		{
			name: "single char slug ignored",
			in:   "@a single is too short",
			want: nil,
		},
		{
			name: "agent name with underscore",
			in:   "@team-x:Agent_One do work",
			want: []WorkspaceMention{{WorkspaceSlug: "team-x", AgentName: "Agent_One"}},
		},
		{
			name: "mention at start of string",
			in:   "@cel ping",
			want: []WorkspaceMention{{WorkspaceSlug: "cel"}},
		},
		{
			name: "mention preceded by punctuation does not match",
			in:   "see this:@something",
			want: nil,
		},
		{
			name: "mixed",
			in:   "@one push, then @two:Bot follow up",
			want: []WorkspaceMention{
				{WorkspaceSlug: "one"},
				{WorkspaceSlug: "two", AgentName: "Bot"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseWorkspaceMentions(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseWorkspaceMentions(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}
