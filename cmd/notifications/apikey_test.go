package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execute runs the apikey command tree against args and returns the error;
// output is swallowed. DB-touching paths are never reached: these tests only
// exercise arg parsing failures.
func execute(args ...string) error {
	cmd := newAPIKeyCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestAPIKeyHasAllSubcommands(t *testing.T) {
	cmd := newAPIKeyCmd()
	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	assert.ElementsMatch(t, []string{"create", "list", "revoke", "set-domains"}, names)
}

func TestAPIKeyCreateRequiresNameAndScopes(t *testing.T) {
	err := execute("create")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
	assert.Contains(t, err.Error(), "scopes")
}

func TestAPIKeyRevokeRequiresID(t *testing.T) {
	require.Error(t, execute("revoke"))
	require.Error(t, execute("revoke", "id-1", "id-2"))
}

func TestAPIKeySetDomainsRequiresIDAndDomains(t *testing.T) {
	err := execute("set-domains")
	require.Error(t, err)

	err = execute("set-domains", "some-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "domains")
}

func TestAPIKeyRejectsUnknownFlags(t *testing.T) {
	require.Error(t, execute("create", "--name", "x", "--scopes", "send", "--bogus"))
}

func TestSplitList(t *testing.T) {
	assert.Equal(t, []string{"a.com", "b.com"}, splitList(" a.com , b.com ,, "))
	assert.Nil(t, splitList(""))
	assert.Nil(t, splitList(" , "))
}
