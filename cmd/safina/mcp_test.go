package main

import (
	"os"
	"os/exec"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("SAFINA_MCP_STDIO_HELPER") != "1" {
		return
	}
	// Exit without the testing runner's PASS output: stdout is protocol-only.
	cmd := mcpCommand()
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestMCPStdioProtocol(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestMCPStdioHelper$") // #nosec G204 -- Current test executable with a fixed helper name; no user command input.
	command.Env = append(os.Environ(), "SAFINA_MCP_STDIO_HELPER=1")
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.CommandTransport{Command: command}, nil)
	require.NoError(t, err)
	tools, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 9)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "approve", Arguments: map[string]any{}})
	require.True(t, err != nil || result.IsError)
	require.NoError(t, session.Close())
}
