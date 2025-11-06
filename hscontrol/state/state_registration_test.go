package state

import (
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/juanfont/headscale/hscontrol/types"
	"github.com/juanfont/headscale/hscontrol/util"
	"github.com/stretchr/testify/require"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func newTestConfig(t *testing.T) *types.Config {
	t.Helper()

	tmpDir := t.TempDir()
	prefixV4 := netip.MustParsePrefix("100.64.0.0/10")
	prefixV6 := netip.MustParsePrefix("fd7a:115c:a1e0::/48")

	return &types.Config{
		Database: types.DatabaseConfig{
			Type: types.DatabaseSqlite,
			Sqlite: types.SqliteConfig{
				Path: filepath.Join(tmpDir, "headscale-test.db"),
			},
		},
		PrefixV4:     &prefixV4,
		PrefixV6:     &prefixV6,
		IPAllocation: types.IPAllocationStrategySequential,
		BaseDomain:   "example.test",
		Policy: types.PolicyConfig{
			Mode: types.PolicyModeDB,
		},
		Tuning: types.Tuning{
			BatchChangeDelay: 10 * time.Millisecond,
		},
	}
}

func TestHandleNodeFromAuthPathHostnameReuseSameUser(t *testing.T) {
	cfg := newTestConfig(t)

	st, err := NewState(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, st.Close())
	})

	user := st.CreateUserForTest("hostname-reuse")
	require.NotNil(t, user)

	hostname := "desktop"
	machineKey1 := key.NewMachine()
	nodeKey1 := key.NewNode()
	discoKey1 := key.NewDisco()

	regEntry1 := types.NewRegisterNode(types.Node{
		MachineKey:     machineKey1.Public(),
		NodeKey:        nodeKey1.Public(),
		DiscoKey:       discoKey1.Public(),
		Hostinfo:       &tailcfg.Hostinfo{Hostname: hostname},
		RegisterMethod: util.RegisterMethodCLI,
	})

	regID1 := types.MustRegistrationID()
	st.SetRegistrationCacheEntry(regID1, regEntry1)

	node1, change1, err := st.HandleNodeFromAuthPath(
		regID1,
		types.UserID(user.ID),
		nil,
		util.RegisterMethodCLI,
	)
	require.NoError(t, err)
	require.True(t, node1.Valid())
	require.False(t, change1.Empty())

	// Second registration with new machine key but same user+hostname.
	machineKey2 := key.NewMachine()
	nodeKey2 := key.NewNode()
	discoKey2 := key.NewDisco()

	regEntry2 := types.NewRegisterNode(types.Node{
		MachineKey:     machineKey2.Public(),
		NodeKey:        nodeKey2.Public(),
		DiscoKey:       discoKey2.Public(),
		Hostinfo:       &tailcfg.Hostinfo{Hostname: hostname},
		RegisterMethod: util.RegisterMethodCLI,
	})

	regID2 := types.MustRegistrationID()
	st.SetRegistrationCacheEntry(regID2, regEntry2)

	node2, change2, err := st.HandleNodeFromAuthPath(
		regID2,
		types.UserID(user.ID),
		nil,
		util.RegisterMethodCLI,
	)
	require.NoError(t, err)
	require.True(t, node2.Valid())
	require.False(t, change2.Empty())

	// Ensure the same node record was reused.
	require.Equal(t, node1.ID(), node2.ID(), "expected hostname reuse to keep existing node ID")
	require.Equal(t, machineKey2.Public(), node2.MachineKey(), "machine key should be updated to new value")
	require.Equal(t, nodeKey2.Public(), node2.NodeKey(), "node key should be updated to new value")
	require.Equal(t, hostname, node2.Hostname())

	// Old machine key should no longer resolve.
	_, ok := st.GetNodeByMachineKey(machineKey1.Public(), types.UserID(user.ID))
	require.False(t, ok, "old machine key should not resolve after reuse")

	// New machine key should resolve to the reused node.
	reusedNode, ok := st.GetNodeByMachineKey(machineKey2.Public(), types.UserID(user.ID))
	require.True(t, ok, "new machine key should resolve after reuse")
	require.Equal(t, node1.ID(), reusedNode.ID(), "reused node ID should match original")

	// Only one node should exist for the user.
	nodesForUser := st.ListNodesByUser(types.UserID(user.ID))
	require.Equal(t, 1, nodesForUser.Len(), "expected only one node for the user after hostname reuse")
}
