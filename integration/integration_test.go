/*
Copyright 2016-2019 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package integration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gravitational/trace"
	"github.com/pkg/sftp"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/exp/slices"

	"github.com/gravitational/teleport"
	"github.com/gravitational/teleport/api/breaker"
	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/constants"
	"github.com/gravitational/teleport/api/defaults"
	apidefaults "github.com/gravitational/teleport/api/defaults"
	tracessh "github.com/gravitational/teleport/api/observability/tracing/ssh"
	"github.com/gravitational/teleport/api/profile"
	"github.com/gravitational/teleport/api/types"
	apievents "github.com/gravitational/teleport/api/types/events"
	apiutils "github.com/gravitational/teleport/api/utils"
	"github.com/gravitational/teleport/api/utils/keypaths"
	"github.com/gravitational/teleport/integration/helpers"
	"github.com/gravitational/teleport/lib"
	"github.com/gravitational/teleport/lib/auth"
	"github.com/gravitational/teleport/lib/auth/testauthority"
	"github.com/gravitational/teleport/lib/bpf"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/events"
	"github.com/gravitational/teleport/lib/events/filesessions"
	"github.com/gravitational/teleport/lib/modules"
	"github.com/gravitational/teleport/lib/pam"
	"github.com/gravitational/teleport/lib/reversetunnel"
	"github.com/gravitational/teleport/lib/service"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/session"
	"github.com/gravitational/teleport/lib/sshutils"
	"github.com/gravitational/teleport/lib/utils"
)

type integrationTestSuite struct {
	helpers.Fixture
}

func newSuite(t *testing.T) *integrationTestSuite {
	return &integrationTestSuite{*helpers.NewFixture(t)}
}

type integrationTest func(t *testing.T, suite *integrationTestSuite)

func (s *integrationTestSuite) bind(test integrationTest) func(t *testing.T) {
	return func(t *testing.T) {
		// Attempt to set a logger for the test. Be warned that parts of the
		// Teleport codebase do not honor the logger passed in via config and
		// will create their own. Do not expect to catch _all_ output with this.
		s.Log = utils.NewLoggerForTests()
		os.RemoveAll(profile.FullProfilePath(""))
		t.Cleanup(func() { s.Log = nil })
		test(t, s)
	}
}

// TestIntegrations acts as the master test suite for all integration tests
// requiring standardized setup and teardown.
func TestIntegrations(t *testing.T) {
	// TODO: break all of these subtests out into individual tests so that we get
	//       better progress reporting, rather than have to wait for the entire
	//       suite to complete
	suite := newSuite(t)

	t.Run("AuditOff", suite.bind(testAuditOff))
	t.Run("AuditOn", suite.bind(testAuditOn))
	t.Run("BPFExec", suite.bind(testBPFExec))
	t.Run("BPFInteractive", suite.bind(testBPFInteractive))
	t.Run("BPFSessionDifferentiation", suite.bind(testBPFSessionDifferentiation))
	t.Run("CmdLabels", suite.bind(testCmdLabels))
	t.Run("ControlMaster", suite.bind(testControlMaster))
	t.Run("CustomReverseTunnel", suite.bind(testCustomReverseTunnel))
	t.Run("DataTransfer", suite.bind(testDataTransfer))
	t.Run("Disconnection", suite.bind(testDisconnectScenarios))
	t.Run("Discovery", suite.bind(testDiscovery))
	t.Run("DiscoveryNode", suite.bind(testDiscoveryNode))
	t.Run("DiscoveryRecovers", suite.bind(testDiscoveryRecovers))
	t.Run("EnvironmentVars", suite.bind(testEnvironmentVariables))
	t.Run("ExecEvents", suite.bind(testExecEvents))
	t.Run("ExternalClient", suite.bind(testExternalClient))
	t.Run("HA", suite.bind(testHA))
	t.Run("Interactive (Regular)", suite.bind(testInteractiveRegular))
	t.Run("Interactive (Reverse Tunnel)", suite.bind(testInteractiveReverseTunnel))
	t.Run("Interoperability", suite.bind(testInteroperability))
	t.Run("InvalidLogin", suite.bind(testInvalidLogins))
	t.Run("JumpTrustedClusters", suite.bind(testJumpTrustedClusters))
	t.Run("JumpTrustedClustersWithLabels", suite.bind(testJumpTrustedClustersWithLabels))
	t.Run("List", suite.bind(testList))
	t.Run("MapRoles", suite.bind(testMapRoles))
	t.Run("MultiplexingTrustedClusters", suite.bind(testMultiplexingTrustedClusters))
	t.Run("PAM", suite.bind(testPAM))
	t.Run("PortForwarding", suite.bind(testPortForwarding))
	t.Run("ProxyHostKeyCheck", suite.bind(testProxyHostKeyCheck))
	t.Run("ReverseTunnelCollapse", suite.bind(testReverseTunnelCollapse))
	t.Run("RotateRollback", suite.bind(testRotateRollback))
	t.Run("RotateSuccess", suite.bind(testRotateSuccess))
	t.Run("RotateTrustedClusters", suite.bind(testRotateTrustedClusters))
	t.Run("SessionStartContainsAccessRequest", suite.bind(testSessionStartContainsAccessRequest))
	t.Run("SessionStreaming", suite.bind(testSessionStreaming))
	t.Run("SSHExitCode", suite.bind(testSSHExitCode))
	t.Run("Shutdown", suite.bind(testShutdown))
	t.Run("TrustedClusters", suite.bind(testTrustedClusters))
	t.Run("TrustedClustersWithLabels", suite.bind(testTrustedClustersWithLabels))
	t.Run("TrustedTunnelNode", suite.bind(testTrustedTunnelNode))
	t.Run("TwoClustersProxy", suite.bind(testTwoClustersProxy))
	t.Run("TwoClustersTunnel", suite.bind(testTwoClustersTunnel))
	t.Run("UUIDBasedProxy", suite.bind(testUUIDBasedProxy))
	t.Run("WindowChange", suite.bind(testWindowChange))
	t.Run("SSHTracker", suite.bind(testSSHTracker))
	t.Run("TestKubeAgentFiltering", suite.bind(testKubeAgentFiltering))
	t.Run("ListResourcesAcrossClusters", suite.bind(testListResourcesAcrossClusters))
	t.Run("SessionRecordingModes", suite.bind(testSessionRecordingModes))
	t.Run("DifferentPinnedIP", suite.bind(testDifferentPinnedIP))
	t.Run("JoinOverReverseTunnelOnly", suite.bind(testJoinOverReverseTunnelOnly))
	t.Run("SFTP", suite.bind(testSFTP))
	t.Run("EscapeSequenceTriggers", suite.bind(testEscapeSequenceTriggers))
	t.Run("AuthLocalNodeControlStream", suite.bind(testAuthLocalNodeControlStream))
}

// testDifferentPinnedIP tests connection is rejected when source IP doesn't match the pinned one
func testDifferentPinnedIP(t *testing.T, suite *integrationTestSuite) {
	modules.SetTestModules(t, &modules.TestModules{TestBuildType: modules.BuildEnterprise})

	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	tconf := suite.defaultServiceConfig()
	tconf.Auth.Enabled = true
	tconf.Proxy.Enabled = true
	tconf.Proxy.DisableWebService = true
	tconf.Proxy.DisableWebInterface = true
	tconf.SSH.Enabled = true
	tconf.SSH.DisableCreateHostUser = true

	teleport := suite.NewTeleportInstance(t)

	role := services.NewImplicitRole()
	ro := role.GetOptions()
	ro.PinSourceIP = true
	role.SetOptions(ro)
	role.SetName("x")
	teleport.AddUserWithRole(suite.Me.Username, role)

	require.NoError(t, teleport.CreateEx(t, nil, tconf))
	require.NoError(t, teleport.Start())
	defer teleport.StopAll()

	site := teleport.GetSiteAPI(helpers.Site)
	require.NotNil(t, site)

	for _, ip := range []string{"1.2.3.4/32", "1843:4545::12/128"} {
		cl, err := teleport.NewClient(helpers.ClientConfig{
			Login:    suite.Me.Username,
			Cluster:  helpers.Site,
			Host:     Host,
			SourceIP: ip,
		})
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		err = cl.SSH(ctx, []string{}, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "ssh: unable to authenticate")
	}
}

// testAuthLocalNodeControlStream verifies some basic expected behaviors for auth-local
// node control streams (requires separate checks because auth-local nodes use a special
// in-memory control stream).
func testAuthLocalNodeControlStream(t *testing.T, suite *integrationTestSuite) {
	const clusterName = "control-stream-test"

	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	tconf := suite.defaultServiceConfig()
	tconf.Auth.Enabled = true
	tconf.Proxy.Enabled = true
	tconf.Proxy.DisableWebService = true
	tconf.Proxy.DisableWebInterface = true
	tconf.SSH.Enabled = true
	tconf.SSH.DisableCreateHostUser = true

	// deliberately create a teleport instance that will end up binding
	// unspecified addr (`0.0.0.0`/`::`). we use this further down to confirm
	// that in-memory control stream can approximate peer-addr substitution.
	teleport := suite.newNamedTeleportInstance(t, clusterName,
		WithNodeName(""),
		WithListeners(helpers.StandardListenerSetupOn("")),
	)

	require.NoError(t, teleport.CreateEx(t, nil, tconf))
	require.NoError(t, teleport.Start())
	defer teleport.StopAll()

	clt := teleport.GetSiteAPI(clusterName)
	require.NotNil(t, clt)

	var nodeID string
	// verify node control stream registers, extracting the id.
	require.Eventually(t, func() bool {
		status, err := clt.GetInventoryStatus(context.Background(), proto.InventoryStatusRequest{
			Connected: true,
		})
		require.NoError(t, err)

		for _, hello := range status.Connected {
			for _, s := range hello.Services {
				if s != types.RoleNode {
					continue
				}
				nodeID = hello.ServerID
				return true
			}
		}
		return false
	}, time.Second*10, time.Millisecond*200)

	var nodeAddr string
	// verify node heartbeat was successful, extracting the addr.
	require.Eventually(t, func() bool {
		node, err := clt.GetNode(context.Background(), defaults.Namespace, nodeID)
		if trace.IsNotFound(err) {
			return false
		}
		require.NoError(t, err)
		nodeAddr = node.GetAddr()
		return true
	}, time.Second*10, time.Millisecond*200)

	addr, err := utils.ParseAddr(nodeAddr)
	require.NoError(t, err)

	// verify that we've replaced the unspecified host.
	require.False(t, addr.IsHostUnspecified())
}

// testAuditOn creates a live session, records a bunch of data through it
// and then reads it back and compares against simulated reality.
func testAuditOn(t *testing.T, suite *integrationTestSuite) {
	tests := []struct {
		comment          string
		inRecordLocation string
		inForwardAgent   bool
		auditSessionsURI string
	}{
		{
			comment:          "normal teleport",
			inRecordLocation: types.RecordAtNode,
			inForwardAgent:   false,
		}, {
			comment:          "recording proxy",
			inRecordLocation: types.RecordAtProxy,
			inForwardAgent:   true,
		}, {
			comment:          "normal teleport with upload to file server",
			inRecordLocation: types.RecordAtNode,
			inForwardAgent:   false,
			auditSessionsURI: t.TempDir(),
		}, {
			comment:          "recording proxy with upload to file server",
			inRecordLocation: types.RecordAtProxy,
			inForwardAgent:   false,
			auditSessionsURI: t.TempDir(),
		}, {
			comment:          "normal teleport, sync recording",
			inRecordLocation: types.RecordAtNodeSync,
			inForwardAgent:   false,
		}, {
			comment:          "recording proxy, sync recording",
			inRecordLocation: types.RecordAtProxySync,
			inForwardAgent:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.comment, func(t *testing.T) {
			tr := utils.NewTracer(utils.ThisFunction()).Start()
			t.Cleanup(func() {
				tr.Stop()
			})

			makeConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
				auditConfig, err := types.NewClusterAuditConfig(types.ClusterAuditConfigSpecV2{
					AuditSessionsURI: tt.auditSessionsURI,
				})
				require.NoError(t, err)

				recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
					Mode: tt.inRecordLocation,
				})
				require.NoError(t, err)

				tconf := suite.defaultServiceConfig()
				tconf.Auth.Enabled = true
				tconf.Auth.AuditConfig = auditConfig
				tconf.Auth.SessionRecordingConfig = recConfig
				tconf.Proxy.Enabled = true
				tconf.Proxy.DisableWebService = true
				tconf.Proxy.DisableWebInterface = true
				tconf.SSH.Enabled = true
				return t, nil, nil, tconf
			}
			teleport := suite.NewTeleportWithConfig(makeConfig())
			t.Cleanup(func() {
				err := teleport.StopAll()
				require.NoError(t, err)
			})

			// Start a node.
			nodeConf := suite.defaultServiceConfig()
			nodeConf.HostUUID = "node"
			nodeConf.Hostname = "node"
			nodeConf.SSH.Enabled = true
			nodeConf.SSH.Addr.Addr = helpers.NewListener(t, service.ListenerNodeSSH, &nodeConf.FileDescriptors)
			_, err := teleport.StartNode(nodeConf)
			require.NoError(t, err)

			// get access to a authClient for the cluster
			site := teleport.GetSiteAPI(helpers.Site)
			require.NotNil(t, site)

			ctx := context.Background()

			// wait 10 seconds for both nodes to show up, otherwise
			// we'll have trouble connecting to the node below.
			waitForNodes := func(site auth.ClientI, count int) error {
				tickCh := time.Tick(500 * time.Millisecond)
				stopCh := time.After(10 * time.Second)
				for {
					select {
					case <-tickCh:
						nodesInSite, err := site.GetNodes(ctx, defaults.Namespace)
						if err != nil && !trace.IsNotFound(err) {
							return trace.Wrap(err)
						}
						if got, want := len(nodesInSite), count; got == want {
							return nil
						}
					case <-stopCh:
						return trace.BadParameter("waited 10s, did find %v nodes", count)
					}
				}
			}
			err = waitForNodes(site, 2)
			require.NoError(t, err)

			// should have no sessions:
			sessions, err := site.GetActiveSessionTrackers(ctx)
			require.NoError(t, err)
			require.Empty(t, sessions)

			// create interactive session (this goroutine is this user's terminal time)
			endC := make(chan error)
			myTerm := NewTerminal(250)
			go func() {
				cl, err := teleport.NewClient(helpers.ClientConfig{
					Login:        suite.Me.Username,
					Cluster:      helpers.Site,
					Host:         Host,
					Port:         helpers.Port(t, nodeConf.SSH.Addr.Addr),
					ForwardAgent: tt.inForwardAgent,
				})
				if err != nil {
					endC <- err
					return
				}
				cl.Stdout = myTerm
				cl.Stdin = myTerm

				err = cl.SSH(context.TODO(), []string{}, false)
				endC <- err
			}()

			// wait until we've found the session in the audit log
			getSession := func(site auth.ClientI) (types.SessionTracker, error) {
				timeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				sessions, err := waitForSessionToBeEstablished(timeout, defaults.Namespace, site)
				if err != nil {
					return nil, trace.Wrap(err)
				}
				return sessions[0], nil
			}
			tracker, err := getSession(site)
			require.NoError(t, err)
			sessionID := tracker.GetSessionID()

			// wait for the user to join this session:
			for len(tracker.GetParticipants()) == 0 {
				time.Sleep(time.Millisecond * 5)
				tracker, err = site.GetSessionTracker(ctx, tracker.GetSessionID())
				require.NoError(t, err)
			}
			// make sure it's us who joined! :)
			require.Equal(t, suite.Me.Username, tracker.GetParticipants()[0].User)

			// let's type "echo hi" followed by "enter" and then "exit" + "enter":
			myTerm.Type("echo hi\n\rexit\n\r")

			// wait for session to end:
			select {
			case err := <-endC:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: Timeout waiting for session to finish.", tt.comment)
			}

			// wait for the upload of the right session to complete
			timeoutC := time.After(10 * time.Second)
		loop:
			for {
				select {
				case event := <-teleport.UploadEventsC:
					if event.SessionID != tracker.GetSessionID() {
						t.Logf("Skipping mismatching session %v, expecting upload of %v.", event.SessionID, tracker.GetSessionID())
						continue
					}
					break loop
				case <-timeoutC:
					dumpGoroutineProfile()
					t.Fatalf("%s: Timeout waiting for upload of session %v to complete to %v",
						tt.comment, tracker.GetSessionID(), tt.auditSessionsURI)
				}
			}

			// read back the entire session (we have to try several times until we get back
			// everything because the session is closing)
			var sessionStream []byte
			for i := 0; i < 6; i++ {
				sessionStream, err = site.GetSessionChunk(apidefaults.Namespace, session.ID(tracker.GetSessionID()), 0, events.MaxChunkBytes)
				require.NoError(t, err)
				if strings.Contains(string(sessionStream), "exit") {
					break
				}
				time.Sleep(time.Millisecond * 250)
				if i >= 5 {
					// session stream keeps coming back short
					t.Fatalf("%s: Stream is not getting data: %q.", tt.comment, string(sessionStream))
				}
			}

			// see what we got. It looks different based on bash settings, but here it is
			// on Ev's machine (hostname is 'edsger'):
			//
			// edsger ~: echo hi
			// hi
			// edsger ~: exit
			// logout
			//
			text := string(sessionStream)
			require.Contains(t, text, "echo hi")
			require.Contains(t, text, "exit")

			// Wait until session.start, session.leave, and session.end events have arrived.
			getSessions := func(site auth.ClientI) ([]events.EventFields, error) {
				tickCh := time.Tick(500 * time.Millisecond)
				stopCh := time.After(10 * time.Second)
				for {
					select {
					case <-tickCh:
						// Get all session events from the backend.
						sessionEvents, err := site.GetSessionEvents(apidefaults.Namespace, session.ID(tracker.GetSessionID()), 0, false)
						if err != nil {
							return nil, trace.Wrap(err)
						}

						// Look through all session events for the three wanted.
						var hasStart bool
						var hasEnd bool
						var hasLeave bool
						for _, se := range sessionEvents {
							if se.GetType() == events.SessionStartEvent {
								hasStart = true
							}
							if se.GetType() == events.SessionEndEvent {
								hasEnd = true
							}
							if se.GetType() == events.SessionLeaveEvent {
								hasLeave = true
							}
						}

						// Make sure all three events were found.
						if hasStart && hasEnd && hasLeave {
							return sessionEvents, nil
						}
					case <-stopCh:
						return nil, trace.BadParameter("unable to find all session events after 10s (mode=%v)", tt.inRecordLocation)
					}
				}
			}
			history, err := getSessions(site)
			require.NoError(t, err)

			getChunk := func(e events.EventFields, maxlen int) string {
				offset := e.GetInt("offset")
				length := e.GetInt("bytes")
				if length == 0 {
					return ""
				}
				if length > maxlen {
					length = maxlen
				}
				return string(sessionStream[offset : offset+length])
			}

			findByType := func(et string) events.EventFields {
				for _, e := range history {
					if e.GetType() == et {
						return e
					}
				}
				return nil
			}

			// there should always be 'session.start' event (and it must be first)
			first := history[0]
			start := findByType(events.SessionStartEvent)
			require.Equal(t, first, start)
			require.Equal(t, 0, start.GetInt("bytes"))
			require.Equal(t, sessionID, start.GetString(events.SessionEventID))
			require.NotEmpty(t, start.GetString(events.TerminalSize))

			// make sure data is recorded properly
			out := &bytes.Buffer{}
			for _, e := range history {
				out.WriteString(getChunk(e, 1000))
			}
			recorded := replaceNewlines(out.String())
			require.Regexp(t, ".*exit.*", recorded)
			require.Regexp(t, ".*echo hi.*", recorded)

			// there should always be 'session.end' event
			end := findByType(events.SessionEndEvent)
			require.NotNil(t, end)
			require.Equal(t, 0, end.GetInt("bytes"))
			require.Equal(t, sessionID, end.GetString(events.SessionEventID))

			// there should always be 'session.leave' event
			leave := findByType(events.SessionLeaveEvent)
			require.NotNil(t, leave)
			require.Equal(t, 0, leave.GetInt("bytes"))
			require.Equal(t, sessionID, leave.GetString(events.SessionEventID))

			// all of them should have a proper time
			for _, e := range history {
				require.False(t, e.GetTime("time").IsZero())
			}
		})
	}
}

// testInteroperability checks if Teleport and OpenSSH behave in the same way
// when executing commands.
func testInteroperability(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	tempdir := t.TempDir()
	tempfile := filepath.Join(tempdir, "file.txt")

	// create new teleport server that will be used by all tests
	teleport := suite.newTeleport(t, nil, true)
	defer teleport.StopAll()

	tests := []struct {
		inCommand   string
		inStdin     string
		outContains string
		outFile     bool
	}{
		// 0 - echo "1\n2\n" | ssh localhost "cat -"
		// this command can be used to copy files by piping stdout to stdin over ssh.
		{
			inCommand:   "cat -",
			inStdin:     "1\n2\n",
			outContains: "1\n2\n",
			outFile:     false,
		},
		// 1 - ssh -tt locahost '/bin/sh -c "mkdir -p /tmp && echo a > /tmp/file.txt"'
		// programs like ansible execute commands like this
		{
			inCommand:   fmt.Sprintf(`/bin/sh -c "mkdir -p /tmp && echo a > %v"`, tempfile),
			inStdin:     "",
			outContains: "a",
			outFile:     true,
		},
		// 2 - ssh localhost tty
		// should print "not a tty"
		{
			inCommand:   "tty",
			inStdin:     "",
			outContains: "not a tty",
			outFile:     false,
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("Test %d: %s", i, strings.Fields(tt.inCommand)[0]), func(t *testing.T) {
			// create new teleport client
			cl, err := teleport.NewClient(helpers.ClientConfig{
				Login:   suite.Me.Username,
				Cluster: helpers.Site,
				Host:    Host,
				Port:    helpers.Port(t, teleport.SSH),
			})
			require.NoError(t, err)

			// hook up stdin and stdout to a buffer for reading and writing
			inbuf := bytes.NewReader([]byte(tt.inStdin))
			outbuf := utils.NewSyncBuffer()
			cl.Stdin = inbuf
			cl.Stdout = outbuf
			cl.Stderr = outbuf

			// run command and wait a maximum of 10 seconds for it to complete
			sessionEndC := make(chan interface{})
			go func() {
				// don't check for err, because sometimes this process should fail
				// with an error and that's what the test is checking for.
				cl.SSH(context.TODO(), []string{tt.inCommand}, false)
				sessionEndC <- true
			}()
			err = waitFor(sessionEndC, time.Second*10)
			require.NoError(t, err)

			// if we are looking for the output in a file, look in the file
			// otherwise check stdout and stderr for the expected output
			if tt.outFile {
				bytes, err := os.ReadFile(tempfile)
				require.NoError(t, err)
				require.Contains(t, string(bytes), tt.outContains)
			} else {
				require.Contains(t, outbuf.String(), tt.outContains)
			}
		})
	}
}

// newUnstartedTeleport helper returns a created but not started Teleport instance pre-configured
// with the current user os.user.Current().
func (s *integrationTestSuite) newUnstartedTeleport(t *testing.T, logins []string, enableSSH bool) *helpers.TeleInstance {
	teleport := s.NewTeleportInstance(t)
	// use passed logins, but use suite's default login if nothing was passed
	if len(logins) == 0 {
		logins = []string{s.Me.Username}
	}
	for _, login := range logins {
		teleport.AddUser(login, []string{login})
	}
	require.NoError(t, teleport.Create(t, nil, enableSSH, nil))
	return teleport
}

// newTeleport helper returns a running Teleport instance pre-configured
// with the current user os.user.Current().
func (s *integrationTestSuite) newTeleport(t *testing.T, logins []string, enableSSH bool) *helpers.TeleInstance {
	teleport := s.newUnstartedTeleport(t, logins, enableSSH)
	require.NoError(t, teleport.Start())
	return teleport
}

// newTeleportIoT helper returns a running Teleport instance with Host as a
// reversetunnel node.
func (s *integrationTestSuite) newTeleportIoT(t *testing.T, logins []string) *helpers.TeleInstance {
	// Create a Teleport instance with Auth/Proxy.
	mainConfig := func() *service.Config {
		tconf := s.defaultServiceConfig()
		tconf.Auth.Enabled = true

		tconf.Proxy.Enabled = true
		tconf.Proxy.DisableWebService = false
		tconf.Proxy.DisableWebInterface = true

		tconf.SSH.Enabled = false

		return tconf
	}
	main := s.NewTeleportWithConfig(t, logins, nil, mainConfig())

	// Create a Teleport instance with a Node.
	nodeConfig := func() *service.Config {
		tconf := s.defaultServiceConfig()
		tconf.Hostname = Host
		tconf.SetToken("token")
		tconf.SetAuthServerAddress(utils.NetAddr{
			AddrNetwork: "tcp",
			Addr:        main.Web,
		})

		tconf.Auth.Enabled = false

		tconf.Proxy.Enabled = false

		tconf.SSH.Enabled = true

		return tconf
	}
	_, err := main.StartReverseTunnelNode(nodeConfig())
	require.NoError(t, err)

	return main
}

func replaceNewlines(in string) string {
	return regexp.MustCompile(`\r?\n`).ReplaceAllString(in, `\n`)
}

// TestUUIDBasedProxy verifies that attempts to proxy to nodes using ambiguous
// hostnames fails with the correct error, and that proxying by UUID succeeds.
func testUUIDBasedProxy(t *testing.T, suite *integrationTestSuite) {
	ctx := context.Background()

	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	teleportSvr := suite.newTeleport(t, nil, true)
	defer teleportSvr.StopAll()

	site := teleportSvr.GetSiteAPI(helpers.Site)

	// addNode adds a node to the teleport instance, returning its uuid.
	// All nodes added this way have the same hostname.
	addNode := func() (string, error) {
		tconf := suite.defaultServiceConfig()
		tconf.Hostname = Host

		tconf.SSH.Enabled = true
		tconf.SSH.Addr.Addr = helpers.NewListenerOn(t, teleportSvr.Hostname, service.ListenerNodeSSH, &tconf.FileDescriptors)

		node, err := teleportSvr.StartNode(tconf)
		if err != nil {
			return "", trace.Wrap(err)
		}

		ident, err := node.GetIdentity(types.RoleNode)
		if err != nil {
			return "", trace.Wrap(err)
		}

		return ident.ID.HostID(), nil
	}

	// add two nodes with the same hostname.
	uuid1, err := addNode()
	require.NoError(t, err)

	uuid2, err := addNode()
	require.NoError(t, err)

	// wait up to 10 seconds for supplied node names to show up.
	waitForNodes := func(site auth.ClientI, nodes ...string) error {
		tickCh := time.Tick(500 * time.Millisecond)
		stopCh := time.After(10 * time.Second)
	Outer:
		for _, nodeName := range nodes {
			for {
				select {
				case <-tickCh:
					nodesInSite, err := site.GetNodes(ctx, defaults.Namespace)
					if err != nil && !trace.IsNotFound(err) {
						return trace.Wrap(err)
					}
					for _, node := range nodesInSite {
						if node.GetName() == nodeName {
							continue Outer
						}
					}
				case <-stopCh:
					return trace.BadParameter("waited 10s, did find node %s", nodeName)
				}
			}
		}
		return nil
	}

	err = waitForNodes(site, uuid1, uuid2)
	require.NoError(t, err)

	// attempting to run a command by hostname should generate NodeIsAmbiguous error.
	_, err = runCommand(t, teleportSvr, []string{"echo", "Hello there!"}, helpers.ClientConfig{Login: suite.Me.Username, Cluster: helpers.Site, Host: Host}, 1)
	require.Error(t, err)
	if !strings.Contains(err.Error(), teleport.NodeIsAmbiguous) {
		require.FailNowf(t, "Expected %s, got %s", teleport.NodeIsAmbiguous, err.Error())
	}

	// attempting to run a command by uuid should succeed.
	_, err = runCommand(t, teleportSvr, []string{"echo", "Hello there!"}, helpers.ClientConfig{Login: suite.Me.Username, Cluster: helpers.Site, Host: uuid1}, 1)
	require.NoError(t, err)
}

// testSSHTracker verifies that an SSH session creates a tracker for sessions.
func testSSHTracker(t *testing.T, suite *integrationTestSuite) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	teleport := suite.newTeleport(t, nil, true)
	defer teleport.StopAll()

	site := teleport.GetSiteAPI(helpers.Site)
	require.NotNil(t, site)

	personA := NewTerminal(250)
	cl, err := teleport.NewClient(helpers.ClientConfig{
		Login:   suite.Me.Username,
		Cluster: helpers.Site,
		Host:    Host,
	})
	require.NoError(t, err)
	cl.Stdout = personA
	cl.Stdin = personA
	personA.Type("\aecho hi\n\r")
	go cl.SSH(ctx, []string{}, false)

	condition := func() bool {
		// verify that the tracker was created
		trackers, err := site.GetActiveSessionTrackers(ctx)
		require.NoError(t, err)
		return len(trackers) == 1
	}

	// wait for the tracker to be created
	require.Eventually(t, condition, time.Minute, time.Millisecond*100)
}

// testInteractive covers SSH into shell and joining the same session from another client
// against a standard teleport node.
func testInteractiveRegular(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	teleport := suite.newTeleport(t, nil, true)
	defer teleport.StopAll()

	verifySessionJoin(t, suite.Me.Username, teleport)
}

// TestInteractiveReverseTunnel covers SSH into shell and joining the same session from another client
// against a reversetunnel node.
func testInteractiveReverseTunnel(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// InsecureDevMode needed for IoT node handshake
	lib.SetInsecureDevMode(true)
	defer lib.SetInsecureDevMode(false)

	teleport := suite.newTeleportIoT(t, nil)
	defer teleport.StopAll()

	verifySessionJoin(t, suite.Me.Username, teleport)
}

func testSessionRecordingModes(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
		Mode: types.RecordAtNode,
	})
	require.NoError(t, err)

	// Enable session recording on node.
	cfg := suite.defaultServiceConfig()
	cfg.Auth.Enabled = true
	cfg.Auth.SessionRecordingConfig = recConfig
	cfg.Proxy.DisableWebService = true
	cfg.Proxy.DisableWebInterface = true
	cfg.Proxy.Enabled = true
	cfg.SSH.Enabled = true

	teleport := suite.NewTeleportWithConfig(t, nil, nil, cfg)
	defer teleport.StopAll()

	// startSession starts an interactive session, users must terminate the
	// session by typing "exit" in the terminal.
	startSession := func(username string) (*Terminal, chan error) {
		term := NewTerminal(250)
		errCh := make(chan error)

		go func() {
			cl, err := teleport.NewClient(helpers.ClientConfig{
				Login:   username,
				Cluster: helpers.Site,
				Host:    Host,
			})
			if err != nil {
				errCh <- trace.Wrap(err)
				return
			}
			cl.Stdout = term
			cl.Stdin = term

			errCh <- cl.SSH(context.TODO(), []string{}, false)
		}()

		return term, errCh
	}

	// waitSessionTermination wait until the errCh returns something and assert
	// it with the provided function.
	waitSessionTermination := func(t *testing.T, errCh chan error, errorAssertion require.ErrorAssertionFunc) {
		errorAssertion(t, waitForError(errCh, 10*time.Second))
	}

	// enableDiskFailure changes the OpenFileFunc on filesession package. The
	// replace function will always return an error when called.
	enableDiskFailure := func() {
		filesessions.SetOpenFileFunc(func(path string, _ int, _ os.FileMode) (*os.File, error) {
			return nil, fmt.Errorf("failed to open file %q", path)
		})
	}

	// disableDiskFailure restore the OpenFileFunc.
	disableDiskFailure := func() {
		filesessions.SetOpenFileFunc(os.OpenFile)
	}

	for name, test := range map[string]struct {
		recordingMode        constants.SessionRecordingMode
		expectSessionFailure bool
	}{
		"BestEffortMode": {
			recordingMode:        constants.SessionRecordingModeBestEffort,
			expectSessionFailure: false,
		},
		"StrictMode": {
			recordingMode:        constants.SessionRecordingModeStrict,
			expectSessionFailure: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Setup user and session recording mode.
			username := suite.Me.Username
			role, err := types.NewRoleV3("devs", types.RoleSpecV5{
				Allow: types.RoleConditions{
					Logins: []string{username},
				},
				Options: types.RoleOptions{
					RecordSession: &types.RecordSession{
						SSH: test.recordingMode,
					},
				},
			})
			require.NoError(t, err)
			require.NoError(t, helpers.SetupUser(teleport.Process, username, []types.Role{role}))

			t.Run("BeforeStartFailure", func(t *testing.T) {
				// Enable disk failure.
				enableDiskFailure()
				defer disableDiskFailure()

				// Start session.
				term, errCh := startSession(username)
				if test.expectSessionFailure {
					waitSessionTermination(t, errCh, require.Error)
					return
				}

				// Send stuff to the session.
				term.Type("echo Hello\n\r")

				// Guarantee the session hasn't stopped after typing.
				select {
				case <-errCh:
					require.Fail(t, "session was closed before")
				default:
				}

				// Wait for the session to terminate without error.
				term.Type("exit\n\r")
				waitSessionTermination(t, errCh, require.NoError)
			})

			t.Run("MidSessionFailure", func(t *testing.T) {
				// Start session.
				term, errCh := startSession(username)

				// Guarantee the session started properly.
				select {
				case <-errCh:
					require.Fail(t, "session was closed before")
				default:
				}

				// Enable disk failure
				enableDiskFailure()
				defer disableDiskFailure()

				// Send stuff to the session.
				term.Type("echo Hello\n\r")

				// Expect the session to fail
				if test.expectSessionFailure {
					waitSessionTermination(t, errCh, require.Error)
					return
				}

				// Wait for the session to terminate without error.
				term.Type("exit\n\r")
				waitSessionTermination(t, errCh, require.NoError)
			})
		})
	}
}

// TestCustomReverseTunnel tests that the SSH node falls back to configured
// proxy address if it cannot connect via the proxy address from the reverse
// tunnel discovery query.
// See https://github.com/gravitational/teleport/issues/4141 for context.
func testCustomReverseTunnel(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// InsecureDevMode needed for IoT node handshake
	lib.SetInsecureDevMode(true)
	defer lib.SetInsecureDevMode(false)

	failingListener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	failingAddr := failingListener.Addr().String()
	failingListener.Close()

	// Create a Teleport instance with Auth/Proxy.
	conf := suite.defaultServiceConfig()
	conf.Auth.Enabled = true
	conf.Proxy.Enabled = true
	conf.Proxy.DisableWebService = false
	conf.Proxy.DisableWebInterface = true
	conf.Proxy.DisableDatabaseProxy = true
	conf.Proxy.TunnelPublicAddrs = []utils.NetAddr{
		{
			// Connect on the address that refuses connection on purpose
			// to test address fallback behavior
			Addr:        failingAddr,
			AddrNetwork: "tcp",
		},
	}
	conf.SSH.Enabled = false

	instanceConfig := suite.DefaultInstanceConfig(t)
	instanceConfig.Listeners = helpers.WebReverseTunnelMuxPortSetup(t, &instanceConfig.Fds)
	main := helpers.NewInstance(t, instanceConfig)

	require.NoError(t, main.CreateEx(t, nil, conf))
	require.NoError(t, main.Start())
	defer main.StopAll()

	// Create a Teleport instance with a Node.
	nodeConf := suite.defaultServiceConfig()
	nodeConf.Hostname = Host
	nodeConf.SetToken("token")
	nodeConf.Auth.Enabled = false
	nodeConf.Proxy.Enabled = false
	nodeConf.SSH.Enabled = true
	t.Setenv(defaults.TunnelPublicAddrEnvar, main.Web)

	// verify the node is able to join the cluster
	_, err = main.StartReverseTunnelNode(nodeConf)
	require.NoError(t, err)
}

// testEscapeSequenceTriggers asserts that both escape handling works, and that
// it can be reliably switched off via config.
func testEscapeSequenceTriggers(t *testing.T, suite *integrationTestSuite) {
	type testCase struct {
		name                  string
		f                     func(t *testing.T, terminal *Terminal, sess <-chan error)
		enableEscapeSequences bool
	}

	testCases := []testCase{
		{
			name:                  "yes",
			f:                     testEscapeSequenceYesTrigger,
			enableEscapeSequences: true,
		},
		{
			name:                  "no",
			f:                     testEscapeSequenceNoTrigger,
			enableEscapeSequences: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			teleport := suite.newTeleport(t, nil, true)
			defer teleport.StopAll()

			site := teleport.GetSiteAPI(helpers.Site)
			require.NotNil(t, site)

			terminal := NewTerminal(250)
			cl, err := teleport.NewClient(helpers.ClientConfig{
				Login:                 suite.Me.Username,
				Cluster:               helpers.Site,
				Host:                  Host,
				EnableEscapeSequences: testCase.enableEscapeSequences,
			})
			require.NoError(t, err)

			cl.Stdout = terminal
			cl.Stdin = terminal
			sess := make(chan error)
			go func() {
				sess <- cl.SSH(ctx, []string{}, false)
			}()

			require.Eventually(t, func() bool {
				trackers, err := site.GetActiveSessionTrackers(ctx)
				require.NoError(t, err)
				return len(trackers) == 1
			}, time.Second*15, time.Millisecond*100)

			select {
			case err := <-sess:
				require.FailNow(t, "session should not have ended", err)
			default:
			}

			testCase.f(t, terminal, sess)
		})
	}
}

func testEscapeSequenceYesTrigger(t *testing.T, terminal *Terminal, sess <-chan error) {
	// Given a running terminal connected to a remote  shell via an active
	// Teleport SSH session, where Teleport has escape sequence processing
	// ENABLED...

	// When I enter some text containing the SSH disconnect escape string
	terminal.Type("\a~.\n\r")

	// Expect that the session will terminate shortly and without error
	select {
	case err := <-sess:
		require.NoError(t, err)
	case <-time.After(time.Second * 15):
		require.FailNow(t, "session should have ended")
	}
}

func testEscapeSequenceNoTrigger(t *testing.T, terminal *Terminal, sess <-chan error) {
	// Given a running terminal connected to a remote shell via an active
	// Teleport SSH session, where Teleport has escape sequence processing
	// DISABLED...

	// When I enter some text containing SSH escape string, followed by some
	// arbitrary text....
	terminal.Type("\a~.\n\r")
	terminal.Type("\aecho made it to here!\n\r")

	// Expect that the session will NOT be disconnected by the escape sequence,
	// and so the arbitrary text will eventually end up in the terminal buffer.
	require.Eventually(t, func() bool {
		select {
		case err := <-sess:
			require.FailNow(t, "Session ended unexpectedly with %v", err)
			return false

		default:
			// if the session didn't end, we should see the output of the last write
			return strings.Contains(terminal.AllOutput(), "made it to here!")
		}
	}, time.Second*15, time.Millisecond*100)

	// When I issue an explicit `exit` command to clean up the remote shell
	terminal.Type("\aexit 0\n\r")

	// Expect that the session will terminate shortly and without error
	select {
	case err := <-sess:
		require.NoError(t, err)
	case <-time.After(time.Second * 15):
		require.FailNow(t, "session should have ended")
	}
}

// verifySessionJoin covers SSH into shell and joining the same session from another client
func verifySessionJoin(t *testing.T, username string, teleport *helpers.TeleInstance) {
	// get a reference to site obj:
	site := teleport.GetSiteAPI(helpers.Site)
	require.NotNil(t, site)

	personA := NewTerminal(250)
	personB := NewTerminal(250)

	// PersonA: SSH into the server, wait one second, then type some commands on stdin:
	sessionA := make(chan error)
	openSession := func() {
		cl, err := teleport.NewClient(helpers.ClientConfig{
			Login:   username,
			Cluster: helpers.Site,
			Host:    Host,
		})
		if err != nil {
			sessionA <- trace.Wrap(err)
			return
		}
		cl.Stdout = personA
		cl.Stdin = personA
		// Person A types something into the terminal (including "exit")
		personA.Type("\aecho hi\n\r\aexit\n\r\a")
		sessionA <- cl.SSH(context.TODO(), []string{}, false)
	}

	// PersonB: wait for a session to become available, then join:
	sessionB := make(chan error)
	joinSession := func() {
		sessionTimeoutCtx, sessionTimeoutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer sessionTimeoutCancel()
		sessions, err := waitForSessionToBeEstablished(sessionTimeoutCtx, defaults.Namespace, site)
		if err != nil {
			sessionB <- trace.Wrap(err)
			return
		}

		sessionID := sessions[0].GetSessionID()
		cl, err := teleport.NewClient(helpers.ClientConfig{
			Login:   username,
			Cluster: helpers.Site,
			Host:    Host,
		})
		if err != nil {
			sessionB <- trace.Wrap(err)
			return
		}

		timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-timeoutCtx.Done():
				sessionB <- timeoutCtx.Err()
				return

			case <-ticker.C:
				err := cl.Join(context.TODO(), types.SessionPeerMode, defaults.Namespace, session.ID(sessionID), personB)
				if err == nil {
					sessionB <- nil
					return
				}
			}
		}
	}

	go openSession()
	go joinSession()

	// wait for the sessions to end
	err := waitForError(sessionA, time.Second*10)
	require.NoError(t, err)

	err = waitForError(sessionB, time.Second*10)
	require.NoError(t, err)

	// make sure the output of B is mirrored in A
	outputOfA := personA.Output(100)
	outputOfB := personB.Output(100)
	require.Contains(t, outputOfA, outputOfB)
}

// TestShutdown tests scenario with a graceful shutdown,
// that session will be working after
func testShutdown(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	teleport := suite.newTeleport(t, nil, true)

	// get a reference to site obj:
	site := teleport.GetSiteAPI(helpers.Site)
	require.NotNil(t, site)

	person := NewTerminal(250)

	cl, err := teleport.NewClient(helpers.ClientConfig{
		Login:   suite.Me.Username,
		Cluster: helpers.Site,
		Host:    Host,
		Port:    helpers.Port(t, teleport.SSH),
	})
	require.NoError(t, err)
	cl.Stdout = person
	cl.Stdin = person

	sshCtx, sshCancel := context.WithCancel(context.Background())
	t.Cleanup(sshCancel)
	sshErr := make(chan error)
	go func() {
		sshErr <- cl.SSH(sshCtx, nil, false)
		sshCancel()
	}()

	retry := func(command, pattern string) {
		person.Type(command)
		// wait for both sites to see each other via their reverse tunnels (for up to 10 seconds)
		abortTime := time.Now().Add(10 * time.Second)
		var matched bool
		var output string
		for {
			output = replaceNewlines(person.Output(1000))
			matched, _ = regexp.MatchString(pattern, output)
			if matched {
				break
			}
			time.Sleep(time.Millisecond * 200)
			if time.Now().After(abortTime) {
				require.FailNowf(t, "failed to capture output: %v", pattern)
			}
		}
		if !matched {
			require.FailNowf(t, "output %q does not match pattern %q", output, pattern)
		}
	}

	retry("echo start \r\n", ".*start.*")

	// initiate shutdown
	ctx := context.TODO()
	shutdownContext := teleport.Process.StartShutdown(ctx)

	require.Eventually(t, func() bool {
		// TODO: check that we either get a connection that fully works or a connection refused error
		c, err := net.DialTimeout("tcp", teleport.ReverseTunnel, 250*time.Millisecond)
		if err != nil {
			require.True(t, utils.IsConnectionRefused(trace.Unwrap(err)))
			return true
		}
		require.NoError(t, c.Close())
		return false
	}, time.Second*5, time.Millisecond*500, "proxy should not accept new connections while shutting down")

	// make sure that terminal still works
	retry("echo howdy \r\n", ".*howdy.*")

	// now type exit and wait for shutdown to complete
	person.Type("exit\n\r")

	select {
	case err := <-sshErr:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "failed to shutdown ssh session")
	}

	select {
	case <-shutdownContext.Done():
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Failed to shut down the server.")
	}
}

// errorVerifier is a function type for functions that check that a given
// error is what was expected. Implementations are expected top return nil
// if the supplied error is as expected, or an descriptive error if is is
// not
type errorVerifier func(error) error

func errorContains(text string) errorVerifier {
	return func(err error) error {
		if err == nil || !strings.Contains(err.Error(), text) {
			return fmt.Errorf("Expected error to contain %q, got: %v", text, err)
		}
		return nil
	}
}

type disconnectTestCase struct {
	recordingMode     string
	options           types.RoleOptions
	disconnectTimeout time.Duration
	concurrentConns   int
	sessCtlTimeout    time.Duration
	postFunc          func(context.Context, *testing.T, *helpers.TeleInstance)

	// verifyError checks if `err` reflects the error expected by the test scenario.
	// It returns nil if yes, non-nil otherwise.
	// It is important for verifyError to not do assertions using `*testing.T`
	// itself, as those assertions must run in the main test goroutine, but
	// verifyError runs in a different goroutine.
	verifyError errorVerifier
}

// TestDisconnectScenarios tests multiple scenarios with client disconnects
func testDisconnectScenarios(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	testCases := []disconnectTestCase{
		{
			recordingMode: types.RecordAtNode,
			options: types.RoleOptions{
				ClientIdleTimeout: types.NewDuration(500 * time.Millisecond),
			},
			disconnectTimeout: time.Second,
		}, {
			recordingMode: types.RecordAtProxy,
			options: types.RoleOptions{
				ForwardAgent:      types.NewBool(true),
				ClientIdleTimeout: types.NewDuration(500 * time.Millisecond),
			},
			disconnectTimeout: time.Second,
		}, {
			recordingMode: types.RecordAtNode,
			options: types.RoleOptions{
				DisconnectExpiredCert: types.NewBool(true),
				MaxSessionTTL:         types.NewDuration(2 * time.Second),
			},
			disconnectTimeout: 4 * time.Second,
		}, {
			recordingMode: types.RecordAtProxy,
			options: types.RoleOptions{
				ForwardAgent:          types.NewBool(true),
				DisconnectExpiredCert: types.NewBool(true),
				MaxSessionTTL:         types.NewDuration(2 * time.Second),
			},
			disconnectTimeout: 4 * time.Second,
		}, {
			// "verify that concurrent connection limits are applied when recording at node",
			recordingMode: types.RecordAtNode,
			options: types.RoleOptions{
				MaxConnections: 1,
			},
			disconnectTimeout: 1 * time.Second,
			concurrentConns:   2,
			verifyError:       errorContains("administratively prohibited"),
		}, {
			// "verify that concurrent connection limits are applied when recording at proxy",
			recordingMode: types.RecordAtProxy,
			options: types.RoleOptions{
				ForwardAgent:   types.NewBool(true),
				MaxConnections: 1,
			},
			disconnectTimeout: 1 * time.Second,
			concurrentConns:   2,
			verifyError:       errorContains("administratively prohibited"),
		}, {
			// "verify that lost connections to auth server terminate controlled conns",
			recordingMode: types.RecordAtNode,
			options: types.RoleOptions{
				MaxConnections: 1,
			},
			disconnectTimeout: time.Second,
			sessCtlTimeout:    500 * time.Millisecond,
			// use postFunc to wait for the semaphore to be acquired and a session
			// to be started, then shut down the auth server.
			postFunc: func(ctx context.Context, t *testing.T, teleport *helpers.TeleInstance) {
				site := teleport.GetSiteAPI(helpers.Site)
				var sems []types.Semaphore
				var err error
				for i := 0; i < 6; i++ {
					sems, err = site.GetSemaphores(ctx, types.SemaphoreFilter{
						SemaphoreKind: types.SemaphoreKindConnection,
					})
					if err == nil && len(sems) > 0 {
						break
					}
					select {
					case <-time.After(time.Millisecond * 100):
					case <-ctx.Done():
						return
					}
				}
				require.NoError(t, err)
				require.Len(t, sems, 1)

				timeoutCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
				defer cancel()

				ss, err := waitForSessionToBeEstablished(timeoutCtx, defaults.Namespace, site)
				require.NoError(t, err)
				require.Len(t, ss, 1)
				require.Nil(t, teleport.StopAuth(false))
			},
		},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("Test %d", i), func(t *testing.T) {
			runDisconnectTest(t, suite, tc)
		})
	}
}

func runDisconnectTest(t *testing.T, suite *integrationTestSuite, tc disconnectTestCase) {
	teleport := suite.NewTeleportInstance(t)

	username := suite.Me.Username
	role, err := types.NewRoleV3("devs", types.RoleSpecV5{
		Options: tc.options,
		Allow: types.RoleConditions{
			Logins: []string{username},
		},
	})
	require.NoError(t, err)
	teleport.AddUserWithRole(username, role)

	netConfig, err := types.NewClusterNetworkingConfigFromConfigFile(types.ClusterNetworkingConfigSpecV2{
		SessionControlTimeout: types.Duration(tc.sessCtlTimeout),
	})
	require.NoError(t, err)

	recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
		Mode: tc.recordingMode,
	})
	require.NoError(t, err)

	cfg := suite.defaultServiceConfig()
	cfg.Auth.Enabled = true
	cfg.Auth.NetworkingConfig = netConfig
	cfg.Auth.SessionRecordingConfig = recConfig
	cfg.Proxy.DisableWebService = true
	cfg.Proxy.DisableWebInterface = true
	cfg.Proxy.Enabled = true
	cfg.SSH.Enabled = true

	require.NoError(t, teleport.CreateEx(t, nil, cfg))
	require.NoError(t, teleport.Start())
	defer teleport.StopAll()

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	if tc.concurrentConns < 1 {
		// test cases that don't specify concurrentConns are single-connection tests.
		tc.concurrentConns = 1
	}

	asyncErrors := make(chan error, 1)

	for i := 0; i < tc.concurrentConns; i++ {
		person := NewTerminal(250)

		openSession := func() {
			defer cancel()
			cl, err := teleport.NewClient(helpers.ClientConfig{
				Login:   username,
				Cluster: helpers.Site,
				Host:    Host,
				Port:    helpers.Port(t, teleport.SSH),
			})
			require.NoError(t, err)
			cl.Stdout = person
			cl.Stdin = person

			err = cl.SSH(ctx, []string{}, false)
			select {
			case <-ctx.Done():
				// either we timed out, or a different session
				// triggered closure.
				return
			default:
			}

			if tc.verifyError != nil {
				if badErrorErr := tc.verifyError(err); badErrorErr != nil {
					asyncErrors <- badErrorErr
				}
			} else if err != nil && !trace.IsEOF(err) && !isSSHError(err) {
				asyncErrors <- fmt.Errorf("expected EOF, ExitError, or nil, got %v instead", err)
				return
			}
		}

		go openSession()

		go func() {
			err := enterInput(ctx, person, "echo start \r\n", ".*start.*")
			if err != nil {
				asyncErrors <- err
			}
		}()
	}

	if tc.postFunc != nil {
		// test case modifies the teleport instance after session start
		tc.postFunc(ctx, t, teleport)
	}

	select {
	case <-time.After(tc.disconnectTimeout + time.Second):
		dumpGoroutineProfile()
		require.FailNowf(t, "timeout", "%s timeout waiting for session to exit: %+v", timeNow(), tc)

	case ae := <-asyncErrors:
		require.FailNow(t, "Async error", ae.Error())

	case <-ctx.Done():
		// session closed.  a test case is successful if the first
		// session to close encountered the expected error variant.
	}
}

func isSSHError(err error) bool {
	switch trace.Unwrap(err).(type) {
	case *ssh.ExitError, *ssh.ExitMissingError:
		return true
	default:
		return false
	}
}

func timeNow() string {
	return time.Now().Format(time.StampMilli)
}

// enterInput simulates entering user input into a terminal and awaiting a
// response. Returns an error if the given response text doesn't match
// the supplied regexp string.
func enterInput(ctx context.Context, person *Terminal, command, pattern string) error {
	person.Type(command)
	abortTime := time.Now().Add(10 * time.Second)
	var matched bool
	var output string
	for {
		output = replaceNewlines(person.Output(1000))
		matched, _ = regexp.MatchString(pattern, output)
		if matched {
			return nil
		}
		select {
		case <-time.After(time.Millisecond * 50):
		case <-ctx.Done():
			// cancellation means that we don't care about the input being
			// confirmed anymore; not equivalent to a timeout.
			return nil
		}
		if time.Now().After(abortTime) {
			return fmt.Errorf("failed to capture pattern %q in %q", pattern, output)
		}
	}
}

// TestInvalidLogins validates that you can't login with invalid login or
// with invalid 'site' parameter
func testEnvironmentVariables(t *testing.T, suite *integrationTestSuite) {
	ctx := context.Background()
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	s := suite.newTeleport(t, nil, true)
	defer s.StopAll()

	// make sure sessions set run command
	tc, err := s.NewClient(helpers.ClientConfig{
		Login:   suite.Me.Username,
		Cluster: helpers.Site,
		Host:    Host,
		Port:    helpers.Port(t, s.SSH),
	})
	require.NoError(t, err)

	// if SessionID is provided, it should be set in the session env vars.
	tc.SessionID = uuid.NewString()
	cmd := []string{"printenv", sshutils.SessionEnvVar}
	out := &bytes.Buffer{}
	tc.Stdout = out
	tc.Stdin = nil
	err = tc.SSH(ctx, cmd, false /* runLocally */)

	require.NoError(t, err)
	require.Equal(t, tc.SessionID, strings.TrimSpace(out.String()))

	// The proxy url should be set in the session env vars.
	cmd = []string{"printenv", teleport.SSHSessionWebproxyAddr}
	out = &bytes.Buffer{}
	tc.Stdout = out
	err = tc.SSH(ctx, cmd, false /* runLocally */)

	require.NoError(t, err)
	require.Equal(t, tc.WebProxyAddr, strings.TrimSpace(out.String()))
}

// TestInvalidLogins validates that you can't login with invalid login or
// with invalid 'site' parameter
func testInvalidLogins(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	instance := suite.newTeleport(t, nil, true)
	defer func() {
		require.NoError(t, instance.StopAll())
	}()

	cmd := []string{"echo", "success"}

	// try the wrong site:
	tc, err := instance.NewClient(helpers.ClientConfig{
		Login:   suite.Me.Username,
		Cluster: "wrong-site",
		Host:    Host,
		Port:    helpers.Port(t, instance.SSH),
	})
	require.NoError(t, err)

	err = tc.SSH(context.Background(), cmd, false)
	require.True(t, trace.IsConnectionProblem(err))
	require.Contains(t, err.Error(), `unknown cluster "wrong-site"`)
}

// TestTwoClustersTunnel creates two teleport clusters: "a" and "b" and creates a
// tunnel from A to B.
//
// Two tests are run, first is when both A and B record sessions at nodes. It
// executes an SSH command on A by connecting directly to A and by connecting
// to B via B<->A tunnel. All sessions should end up in A.
//
// In the second test, sessions are recorded at B. All sessions still show up on
// A (they are Teleport nodes) but in addition, two show up on B when connecting
// over the B<->A tunnel because sessions are recorded at the proxy.
func testTwoClustersTunnel(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	now := time.Now().In(time.UTC).Round(time.Second)

	tests := []struct {
		inRecordLocation  string
		outExecCountSiteA int
		outExecCountSiteB int
	}{
		// normal teleport. since all events are recorded at the node, all events
		// end up on site-a and none on site-b.
		{
			types.RecordAtNode,
			3,
			0,
		},
		// recording proxy. since events are recorded at the proxy, 3 events end up
		// on site-a (because it's a teleport node so it still records at the node)
		// and 2 events end up on site-b because it's recording.
		{
			types.RecordAtProxy,
			3,
			2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.inRecordLocation, func(t *testing.T) {
			twoClustersTunnel(t, suite, now, tt.inRecordLocation, tt.outExecCountSiteA, tt.outExecCountSiteB)
		})
	}

	log.Info("Tests done. Cleaning up.")
}

func twoClustersTunnel(t *testing.T, suite *integrationTestSuite, now time.Time, proxyRecordMode string, execCountSiteA, execCountSiteB int) {
	// start the http proxy, we need to make sure this was not used
	ph := &helpers.ProxyHandler{}
	ts := httptest.NewServer(ph)
	defer ts.Close()

	// clear out any proxy environment variables
	for _, v := range []string{"http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY"} {
		t.Setenv(v, "")
	}

	username := suite.Me.Username

	a := suite.newNamedTeleportInstance(t, "site-A")
	b := suite.newNamedTeleportInstance(t, "site-B")

	a.AddUser(username, []string{username})
	b.AddUser(username, []string{username})

	recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
		Mode: proxyRecordMode,
	})
	require.NoError(t, err)

	acfg := suite.defaultServiceConfig()
	acfg.Auth.Enabled = true
	acfg.Proxy.Enabled = true
	acfg.Proxy.DisableWebService = true
	acfg.Proxy.DisableWebInterface = true
	acfg.SSH.Enabled = true

	bcfg := suite.defaultServiceConfig()
	bcfg.Auth.Enabled = true
	bcfg.Auth.SessionRecordingConfig = recConfig
	bcfg.Proxy.Enabled = true
	bcfg.Proxy.DisableWebService = true
	bcfg.Proxy.DisableWebInterface = true
	bcfg.SSH.Enabled = false

	require.NoError(t, b.CreateEx(t, a.Secrets.AsSlice(), bcfg))
	t.Cleanup(func() { require.NoError(t, b.StopAll()) })

	require.NoError(t, a.CreateEx(t, b.Secrets.AsSlice(), acfg))
	t.Cleanup(func() { require.NoError(t, a.StopAll()) })

	require.NoError(t, b.Start())
	require.NoError(t, a.Start())

	// The Listener FDs injected into SiteA will be closed when SiteA restarts
	// later in in the test, rendering them all invalid. This will make SiteA
	// fail when it attempts to start back up again. We can't just inject a
	// totally new listener config into SiteA when it restarts, or SiteB won't
	// be able to find it.
	//
	// The least bad option is to duplicate all of SiteA's Listener FDs and
	// inject those duplicates prior to restarting the SiteA cluster.
	aFdCache, err := a.Process.ExportFileDescriptors()
	require.NoError(t, err)

	// Wait for both cluster to see each other via reverse tunnels.
	require.Eventually(t, helpers.WaitForClusters(a.Tunnel, 2), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")
	require.Eventually(t, helpers.WaitForClusters(b.Tunnel, 2), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")

	var (
		outputA bytes.Buffer
		outputB bytes.Buffer
	)

	// make sure the direct dialer was used and not the proxy dialer
	require.Zero(t, ph.Count())

	// if we got here, it means two sites are cross-connected. lets execute SSH commands
	sshPort := helpers.Port(t, a.SSH)
	cmd := []string{"echo", "hello world"}

	// directly:
	tc, err := a.NewClient(helpers.ClientConfig{
		Login:        username,
		Cluster:      a.Secrets.SiteName,
		Host:         Host,
		Port:         sshPort,
		ForwardAgent: true,
	})
	tc.Stdout = &outputA
	require.NoError(t, err)
	err = tc.SSH(context.TODO(), cmd, false)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", outputA.String())

	// Update trusted CAs.
	err = tc.UpdateTrustedCA(context.TODO(), a.Secrets.SiteName)
	require.NoError(t, err)

	// The known_hosts file should have two certificates, the way bytes.Split
	// works that means the output will be 3 (2 certs + 1 empty).
	buffer, err := os.ReadFile(keypaths.KnownHostsPath(tc.KeysDir))
	require.NoError(t, err)
	parts := bytes.Split(buffer, []byte("\n"))
	require.Len(t, parts, 3)

	roots := x509.NewCertPool()
	werr := filepath.Walk(keypaths.CAsDir(tc.KeysDir, Host), func(path string, info fs.FileInfo, err error) error {
		require.NoError(t, err)
		if info.IsDir() {
			return nil
		}
		buffer, err = os.ReadFile(path)
		require.NoError(t, err)
		ok := roots.AppendCertsFromPEM(buffer)
		require.True(t, ok)
		return nil
	})
	require.NoError(t, werr)
	ok := roots.AppendCertsFromPEM(buffer)
	require.True(t, ok)

	// wait for active tunnel connections to be established
	helpers.WaitForActiveTunnelConnections(t, b.Tunnel, a.Secrets.SiteName, 1)

	// via tunnel b->a:
	tc, err = b.NewClient(helpers.ClientConfig{
		Login:        username,
		Cluster:      a.Secrets.SiteName,
		Host:         Host,
		Port:         sshPort,
		ForwardAgent: true,
	})
	tc.Stdout = &outputB
	require.NoError(t, err)
	err = tc.SSH(context.TODO(), cmd, false)
	require.NoError(t, err)
	require.Equal(t, outputA.String(), outputB.String())

	// Stop "site-A" and try to connect to it again via "site-A" (expect a connection error)
	require.NoError(t, a.StopAuth(false))
	err = tc.SSH(context.TODO(), cmd, false)
	require.IsType(t, err, trace.ConnectionProblem(nil, ""))

	// Reset and start "Site-A" again
	a.Config.FileDescriptors = aFdCache
	require.NoError(t, a.Reset())
	require.NoError(t, a.Start())

	// try to execute an SSH command using the same old client to helpers.Site-B
	// "site-A" and "site-B" reverse tunnels are supposed to reconnect,
	// and 'tc' (client) is also supposed to reconnect
	var sshErr error
	tcHasReconnected := func() bool {
		sshErr = tc.SSH(context.TODO(), cmd, false)
		return sshErr == nil
	}
	require.Eventually(t, tcHasReconnected, 10*time.Second, 250*time.Millisecond,
		"Timed out waiting for helpers.Site A to restart: %v", sshErr)

	clientHasEvents := func(site auth.ClientI, count int) func() bool {
		// only look for exec events
		eventTypes := []string{events.ExecEvent}

		return func() bool {
			eventsInSite, _, err := site.SearchEvents(now, now.Add(1*time.Hour), defaults.Namespace, eventTypes, 0, types.EventOrderAscending, "")
			require.NoError(t, err)
			return len(eventsInSite) == count
		}
	}

	siteA := a.GetSiteAPI(a.Secrets.SiteName)
	require.Eventually(t, clientHasEvents(siteA, execCountSiteA), 5*time.Second, 500*time.Millisecond,
		"Failed to find %d events on helpers.Site A after 5s", execCountSiteA)

	siteB := b.GetSiteAPI(b.Secrets.SiteName)
	require.Eventually(t, clientHasEvents(siteB, execCountSiteB), 5*time.Second, 500*time.Millisecond,
		"Failed to find %d events on helpers.Site B after 5s", execCountSiteB)
}

// TestTwoClustersProxy checks if the reverse tunnel uses a HTTP PROXY to
// establish a connection.
func testTwoClustersProxy(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// start the http proxy
	ps := &helpers.ProxyHandler{}
	ts := httptest.NewServer(ps)
	defer ts.Close()

	// set the http_proxy environment variable
	u, err := url.Parse(ts.URL)
	require.NoError(t, err)
	t.Setenv("http_proxy", u.Host)

	username := suite.Me.Username

	// httpproxy doesn't allow proxying when the target is localhost, so use
	// this address instead.
	addr, err := helpers.GetLocalIP()
	require.NoError(t, err)
	a := suite.newNamedTeleportInstance(t, "site-A",
		WithNodeName(addr),
		WithListeners(helpers.StandardListenerSetupOn(addr)),
	)
	b := suite.newNamedTeleportInstance(t, "site-B",
		WithNodeName(addr),
		WithListeners(helpers.StandardListenerSetupOn(addr)),
	)

	a.AddUser(username, []string{username})
	b.AddUser(username, []string{username})

	require.NoError(t, b.Create(t, a.Secrets.AsSlice(), false, nil))
	defer b.StopAll()
	require.NoError(t, a.Create(t, b.Secrets.AsSlice(), true, nil))
	defer a.StopAll()

	require.NoError(t, b.Start())
	require.NoError(t, a.Start())

	// Wait for both cluster to see each other via reverse tunnels.
	require.Eventually(t, helpers.WaitForClusters(a.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")
	require.Eventually(t, helpers.WaitForClusters(b.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")

	// make sure the reverse tunnel went through the proxy
	require.Greater(t, ps.Count(), 0, "proxy did not intercept any connection")

	// stop both sites for real
	require.NoError(t, b.StopAll())
	require.NoError(t, a.StopAll())
}

// TestHA tests scenario when auth server for the cluster goes down
// and we switch to local persistent caches
func testHA(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	username := suite.Me.Username

	a := suite.newNamedTeleportInstance(t, "cluster-a")
	b := suite.newNamedTeleportInstance(t, "cluster-b")

	a.AddUser(username, []string{username})
	b.AddUser(username, []string{username})

	require.NoError(t, b.Create(t, a.Secrets.AsSlice(), true, nil))
	require.NoError(t, a.Create(t, b.Secrets.AsSlice(), true, nil))

	require.NoError(t, b.Start())
	require.NoError(t, a.Start())

	// The Listener FDs injected into SiteA will be closed when SiteA restarts
	// later in in the test, rendering them all invalid. This will make SiteA
	// fail when it attempts to start back up again. We can't just inject a
	// totally new listener config into SiteA when it restarts, or SiteB won't
	// be able to find it.
	//
	// The least bad option is to duplicate all of SiteA's Listener FDs and
	// inject those duplicates prior to restarting the SiteA cluster.
	aFdCache, err := a.Process.ExportFileDescriptors()
	require.NoError(t, err)

	sshPort, _, _ := a.StartNodeAndProxy(t, "cluster-a-node")

	// Wait for both cluster to see each other via reverse tunnels.
	require.Eventually(t, helpers.WaitForClusters(a.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")
	require.Eventually(t, helpers.WaitForClusters(b.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")

	cmd := []string{"echo", "hello world"}
	tc, err := b.NewClient(helpers.ClientConfig{
		Login:   username,
		Cluster: "cluster-a",
		Host:    Loopback,
		Port:    sshPort,
	})
	require.NoError(t, err)

	output := &bytes.Buffer{}
	tc.Stdout = output
	// try to execute an SSH command using the same old client  to helpers.Site-B
	// "site-A" and "site-B" reverse tunnels are supposed to reconnect,
	// and 'tc' (client) is also supposed to reconnect
	for i := 0; i < 10; i++ {
		time.Sleep(time.Millisecond * 50)
		err = tc.SSH(context.TODO(), cmd, false)
		if err == nil {
			break
		}
	}
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output.String())

	// Stop cluster "a" to force existing tunnels to close.
	require.NoError(t, a.StopAuth(true))

	// Reset KeyPair set by the first start by ACME. After introducing the ALPN TLS listener TLS proxy
	// certs are generated even if WebService and WebInterface was disabled and only DisableTLS
	// flag skips the TLS cert initialization. the First start call creates the ACME certs
	// where Resets() call deletes certs dir thus KeyPairs is no longer valid.
	a.Config.Proxy.KeyPairs = nil

	// Restart cluster "a".
	a.Config.FileDescriptors = aFdCache
	require.NoError(t, a.Reset())
	require.NoError(t, a.Start())

	// Wait for both cluster to see each other via reverse tunnels.
	require.Eventually(t, helpers.WaitForClusters(a.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")
	require.Eventually(t, helpers.WaitForClusters(b.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")

	// try to execute an SSH command using the same old client to site-B
	// "site-A" and "site-B" reverse tunnels are supposed to reconnect,
	// and 'tc' (client) is also supposed to reconnect
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)
		err = tc.SSH(context.TODO(), cmd, false)
		if err == nil {
			break
		}
	}
	require.NoError(t, err)

	// stop cluster and remaining nodes
	require.NoError(t, a.StopAll())
	require.NoError(t, b.StopAll())
}

// TestMapRoles tests local to remote role mapping and access patterns
func testMapRoles(t *testing.T, suite *integrationTestSuite) {
	ctx := context.Background()
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	username := suite.Me.Username

	clusterMain := "cluster-main"
	clusterAux := "cluster-aux"

	main := suite.newNamedTeleportInstance(t, clusterMain)
	aux := suite.newNamedTeleportInstance(t, clusterAux)

	// main cluster has a local user and belongs to role "main-devs"
	mainDevs := "main-devs"
	role, err := types.NewRoleV3(mainDevs, types.RoleSpecV5{
		Allow: types.RoleConditions{
			Logins: []string{username},
		},
	})
	require.NoError(t, err)
	main.AddUserWithRole(username, role)

	// for role mapping test we turn on Web API on the main cluster
	// as it's used
	makeConfig := func(enableSSH bool) (*testing.T, []*helpers.InstanceSecrets, *service.Config) {
		tconf := suite.defaultServiceConfig()
		tconf.SSH.Enabled = enableSSH
		tconf.Proxy.DisableWebService = false
		tconf.Proxy.DisableWebInterface = true
		return t, nil, tconf
	}
	lib.SetInsecureDevMode(true)
	defer lib.SetInsecureDevMode(false)

	require.NoError(t, main.CreateEx(makeConfig(false)))
	require.NoError(t, aux.CreateEx(makeConfig(true)))

	// auxiliary cluster has a role aux-devs
	// connect aux cluster to main cluster
	// using trusted clusters, so remote user will be allowed to assume
	// role specified by mapping remote role "devs" to local role "local-devs"
	auxDevs := "aux-devs"
	role, err = types.NewRoleV3(auxDevs, types.RoleSpecV5{
		Allow: types.RoleConditions{
			Logins: []string{username},
		},
	})
	require.NoError(t, err)
	err = aux.Process.GetAuthServer().UpsertRole(ctx, role)
	require.NoError(t, err)
	trustedClusterToken := "trusted-cluster-token"
	err = main.Process.GetAuthServer().UpsertToken(ctx,
		services.MustCreateProvisionToken(trustedClusterToken, []types.SystemRole{types.RoleTrustedCluster}, time.Time{}))
	require.NoError(t, err)
	trustedCluster := main.AsTrustedCluster(trustedClusterToken, types.RoleMap{
		{Remote: mainDevs, Local: []string{auxDevs}},
	})

	// modify trusted cluster resource name so it would not
	// match the cluster name to check that it does not matter
	trustedCluster.SetName(main.Secrets.SiteName + "-cluster")

	require.NoError(t, main.Start())
	require.NoError(t, aux.Start())

	err = trustedCluster.CheckAndSetDefaults()
	require.NoError(t, err)

	// try and upsert a trusted cluster
	helpers.TryCreateTrustedCluster(t, aux.Process.GetAuthServer(), trustedCluster)
	helpers.WaitForTunnelConnections(t, main.Process.GetAuthServer(), clusterAux, 1)

	sshPort, _, _ := aux.StartNodeAndProxy(t, "aux-node")

	// Wait for both cluster to see each other via reverse tunnels.
	require.Eventually(t, helpers.WaitForClusters(main.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")
	require.Eventually(t, helpers.WaitForClusters(aux.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")

	// Make sure that GetNodes returns nodes in the remote site. This makes
	// sure identity aware GetNodes works for remote clusters. Testing of the
	// correct nodes that identity aware GetNodes is done in TestList.
	var nodes []types.Server
	for i := 0; i < 10; i++ {
		nodes, err = aux.Process.GetAuthServer().GetNodes(ctx, defaults.Namespace)
		require.NoError(t, err)
		if len(nodes) != 2 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
	}
	require.Len(t, nodes, 2)

	cmd := []string{"echo", "hello world"}
	tc, err := main.NewClient(helpers.ClientConfig{
		Login:   username,
		Cluster: clusterAux,
		Host:    Loopback,
		Port:    sshPort,
	})
	require.NoError(t, err)
	output := &bytes.Buffer{}
	tc.Stdout = output
	require.NoError(t, err)
	// try to execute an SSH command using the same old client  to helpers.Site-B
	// "site-A" and "site-B" reverse tunnels are supposed to reconnect,
	// and 'tc' (client) is also supposed to reconnect
	for i := 0; i < 10; i++ {
		time.Sleep(time.Millisecond * 50)
		err = tc.SSH(context.TODO(), cmd, false)
		if err == nil {
			break
		}
	}
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output.String())

	// make sure both clusters have the right certificate authorities with the right signing keys.
	tests := []struct {
		name                       string
		mainClusterName            string
		auxClusterName             string
		inCluster                  *helpers.TeleInstance
		outChkMainUserCA           require.ErrorAssertionFunc
		outChkMainUserCAPrivateKey require.ValueAssertionFunc
		outChkMainHostCA           require.ErrorAssertionFunc
		outChkMainHostCAPrivateKey require.ValueAssertionFunc
		outChkAuxUserCA            require.ErrorAssertionFunc
		outChkAuxUserCAPrivateKey  require.ValueAssertionFunc
		outChkAuxHostCA            require.ErrorAssertionFunc
		outChkAuxHostCAPrivateKey  require.ValueAssertionFunc
	}{
		// 0 - main
		//   * User CA for main has one signing key.
		//   * Host CA for main has one signing key.
		//   * User CA for aux does not exist.
		//   * Host CA for aux has no signing keys.
		{
			name:                       "main",
			mainClusterName:            main.Secrets.SiteName,
			auxClusterName:             aux.Secrets.SiteName,
			inCluster:                  main,
			outChkMainUserCA:           require.NoError,
			outChkMainUserCAPrivateKey: require.NotEmpty,
			outChkMainHostCA:           require.NoError,
			outChkMainHostCAPrivateKey: require.NotEmpty,
			outChkAuxUserCA:            require.Error,
			outChkAuxUserCAPrivateKey:  require.Empty,
			outChkAuxHostCA:            require.NoError,
			outChkAuxHostCAPrivateKey:  require.Empty,
		},
		// 1 - aux
		//   * User CA for main has no signing keys.
		//   * Host CA for main has no signing keys.
		//   * User CA for aux has one signing key.
		//   * Host CA for aux has one signing key.
		{
			name:                       "aux",
			mainClusterName:            trustedCluster.GetName(),
			auxClusterName:             aux.Secrets.SiteName,
			inCluster:                  aux,
			outChkMainUserCA:           require.NoError,
			outChkMainUserCAPrivateKey: require.Empty,
			outChkMainHostCA:           require.NoError,
			outChkMainHostCAPrivateKey: require.Empty,
			outChkAuxUserCA:            require.NoError,
			outChkAuxUserCAPrivateKey:  require.NotEmpty,
			outChkAuxHostCA:            require.NoError,
			outChkAuxHostCAPrivateKey:  require.NotEmpty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cid := types.CertAuthID{Type: types.UserCA, DomainName: tt.mainClusterName}
			mainUserCAs, err := tt.inCluster.Process.GetAuthServer().GetCertAuthority(ctx, cid, true)
			tt.outChkMainUserCA(t, err)
			if err == nil {
				tt.outChkMainUserCAPrivateKey(t, mainUserCAs.GetActiveKeys().SSH[0].PrivateKey)
			}

			cid = types.CertAuthID{Type: types.HostCA, DomainName: tt.mainClusterName}
			mainHostCAs, err := tt.inCluster.Process.GetAuthServer().GetCertAuthority(ctx, cid, true)
			tt.outChkMainHostCA(t, err)
			if err == nil {
				tt.outChkMainHostCAPrivateKey(t, mainHostCAs.GetActiveKeys().SSH[0].PrivateKey)
			}

			cid = types.CertAuthID{Type: types.UserCA, DomainName: tt.auxClusterName}
			auxUserCAs, err := tt.inCluster.Process.GetAuthServer().GetCertAuthority(ctx, cid, true)
			tt.outChkAuxUserCA(t, err)
			if err == nil {
				tt.outChkAuxUserCAPrivateKey(t, auxUserCAs.GetActiveKeys().SSH[0].PrivateKey)
			}

			cid = types.CertAuthID{Type: types.HostCA, DomainName: tt.auxClusterName}
			auxHostCAs, err := tt.inCluster.Process.GetAuthServer().GetCertAuthority(ctx, cid, true)
			tt.outChkAuxHostCA(t, err)
			if err == nil {
				tt.outChkAuxHostCAPrivateKey(t, auxHostCAs.GetActiveKeys().SSH[0].PrivateKey)
			}
		})
	}

	// stop clusters and remaining nodes
	require.NoError(t, main.StopAll())
	require.NoError(t, aux.StopAll())
}

// trustedClusterTest is a test setup for trusted clusters tests
type trustedClusterTest struct {
	// multiplex sets up multiplexing of the reversetunnel SSH
	// socket and the proxy's web socket
	multiplex bool
	// useJumpHost turns on jump host mode for the access
	// to the proxy instead of the proxy command
	useJumpHost bool
	// useLabels turns on trusted cluster labels and
	// verifies RBAC
	useLabels bool
}

// TestTrustedClusters tests remote clusters scenarios
// using trusted clusters feature
func testTrustedClusters(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	trustedClusters(t, suite, trustedClusterTest{multiplex: false})
}

// TestTrustedClustersWithLabels tests remote clusters scenarios
// using trusted clusters feature and access labels
func testTrustedClustersWithLabels(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	trustedClusters(t, suite, trustedClusterTest{multiplex: false, useLabels: true})
}

// TestJumpTrustedClusters tests remote clusters scenarios
// using trusted clusters feature using jumphost connection
func testJumpTrustedClusters(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	trustedClusters(t, suite, trustedClusterTest{multiplex: false, useJumpHost: true})
}

// TestJumpTrustedClusters tests remote clusters scenarios
// using trusted clusters feature using jumphost connection
func testJumpTrustedClustersWithLabels(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	trustedClusters(t, suite, trustedClusterTest{multiplex: false, useJumpHost: true, useLabels: true})
}

// TestMultiplexingTrustedClusters tests remote clusters scenarios
// using trusted clusters feature
func testMultiplexingTrustedClusters(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	trustedClusters(t, suite, trustedClusterTest{multiplex: true})
}

func standardPortsOrMuxSetup(t *testing.T, mux bool, fds *[]service.FileDescriptor) *helpers.InstanceListeners {
	if mux {
		return helpers.WebReverseTunnelMuxPortSetup(t, fds)
	}
	return helpers.StandardListenerSetup(t, fds)
}

func trustedClusters(t *testing.T, suite *integrationTestSuite, test trustedClusterTest) {
	ctx := context.Background()
	username := suite.Me.Username

	clusterMain := "cluster-main"
	clusterAux := "cluster-aux"
	mainCfg := helpers.InstanceConfig{
		ClusterName: clusterMain,
		HostID:      helpers.HostID,
		NodeName:    Host,
		Priv:        suite.Priv,
		Pub:         suite.Pub,
		Log:         suite.Log,
	}
	mainCfg.Listeners = standardPortsOrMuxSetup(t, test.multiplex, &mainCfg.Fds)
	main := helpers.NewInstance(t, mainCfg)
	aux := suite.newNamedTeleportInstance(t, clusterAux)

	// main cluster has a local user and belongs to role "main-devs" and "main-admins"
	mainDevs := "main-devs"
	devsRole, err := types.NewRoleV3(mainDevs, types.RoleSpecV5{
		Allow: types.RoleConditions{
			Logins: []string{username},
		},
	})
	// If the test is using labels, the cluster will be labeled
	// and user will be granted access if labels match.
	// Otherwise, to preserve backwards-compatibility
	// roles with no labels will grant access to clusters with no labels.
	if test.useLabels {
		devsRole.SetClusterLabels(types.Allow, types.Labels{"access": []string{"prod"}})
	}
	require.NoError(t, err)

	mainAdmins := "main-admins"
	adminsRole, err := types.NewRoleV3(mainAdmins, types.RoleSpecV5{
		Allow: types.RoleConditions{
			Logins: []string{"superuser"},
		},
	})
	require.NoError(t, err)

	main.AddUserWithRole(username, devsRole, adminsRole)

	// Ops users can only access remote clusters with label 'access': 'ops'
	mainOps := "main-ops"
	mainOpsRole, err := types.NewRoleV3(mainOps, types.RoleSpecV5{
		Allow: types.RoleConditions{
			Logins:        []string{username},
			ClusterLabels: types.Labels{"access": []string{"ops"}},
		},
	})
	require.NoError(t, err)
	main.AddUserWithRole(mainOps, mainOpsRole, adminsRole)

	// for role mapping test we turn on Web API on the main cluster
	// as it's used
	makeConfig := func(enableSSH bool) (*testing.T, []*helpers.InstanceSecrets, *service.Config) {
		tconf := suite.defaultServiceConfig()
		tconf.Proxy.DisableWebService = false
		tconf.Proxy.DisableWebInterface = true
		tconf.SSH.Enabled = enableSSH
		return t, nil, tconf
	}
	lib.SetInsecureDevMode(true)
	defer lib.SetInsecureDevMode(false)

	require.NoError(t, main.CreateEx(makeConfig(false)))
	require.NoError(t, aux.CreateEx(makeConfig(true)))

	// auxiliary cluster has only a role aux-devs
	// connect aux cluster to main cluster
	// using trusted clusters, so remote user will be allowed to assume
	// role specified by mapping remote role "devs" to local role "local-devs"
	auxDevs := "aux-devs"
	auxRole, err := types.NewRoleV3(auxDevs, types.RoleSpecV5{
		Allow: types.RoleConditions{
			Logins: []string{username},
		},
	})
	require.NoError(t, err)
	err = aux.Process.GetAuthServer().UpsertRole(ctx, auxRole)
	require.NoError(t, err)

	trustedClusterToken := "trusted-cluster-token"
	tokenResource, err := types.NewProvisionToken(trustedClusterToken, []types.SystemRole{types.RoleTrustedCluster}, time.Time{})
	require.NoError(t, err)
	if test.useLabels {
		meta := tokenResource.GetMetadata()
		meta.Labels = map[string]string{"access": "prod"}
		tokenResource.SetMetadata(meta)
	}
	err = main.Process.GetAuthServer().UpsertToken(ctx, tokenResource)
	require.NoError(t, err)
	// Note that the mapping omits admins role, this is to cover the scenario
	// when root cluster and leaf clusters have different role sets
	trustedCluster := main.AsTrustedCluster(trustedClusterToken, types.RoleMap{
		{Remote: mainDevs, Local: []string{auxDevs}},
		{Remote: mainOps, Local: []string{auxDevs}},
	})

	// modify trusted cluster resource name, so it would not
	// match the cluster name to check that it does not matter
	trustedCluster.SetName(main.Secrets.SiteName + "-cluster")

	require.NoError(t, main.Start())
	require.NoError(t, aux.Start())

	err = trustedCluster.CheckAndSetDefaults()
	require.NoError(t, err)

	// try and upsert a trusted cluster
	helpers.TryCreateTrustedCluster(t, aux.Process.GetAuthServer(), trustedCluster)
	helpers.WaitForTunnelConnections(t, main.Process.GetAuthServer(), clusterAux, 1)

	sshPort, _, _ := aux.StartNodeAndProxy(t, "aux-node")

	// Wait for both cluster to see each other via reverse tunnels.
	require.Eventually(t, helpers.WaitForClusters(main.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")
	require.Eventually(t, helpers.WaitForClusters(aux.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")

	cmd := []string{"echo", "hello world"}

	// Try and connect to a node in the Aux cluster from the Main cluster using
	// direct dialing.
	creds, err := helpers.GenerateUserCreds(helpers.UserCredsRequest{
		Process:        main.Process,
		Username:       username,
		RouteToCluster: clusterAux,
	})
	require.NoError(t, err)

	tc, err := main.NewClientWithCreds(helpers.ClientConfig{
		Login:    username,
		Cluster:  clusterAux,
		Host:     Loopback,
		Port:     sshPort,
		JumpHost: test.useJumpHost,
	}, *creds)
	require.NoError(t, err)

	// tell the client to trust aux cluster CAs (from secrets). this is the
	// equivalent of 'known hosts' in openssh
	auxCAS, err := aux.Secrets.GetCAs()
	require.NoError(t, err)
	for _, auxCA := range auxCAS {
		err = tc.AddTrustedCA(ctx, auxCA)
		require.NoError(t, err)
	}

	output := &bytes.Buffer{}
	tc.Stdout = output
	require.NoError(t, err)
	for i := 0; i < 10; i++ {
		time.Sleep(time.Millisecond * 50)
		err = tc.SSH(ctx, cmd, false)
		if err == nil {
			break
		}
	}
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output.String())

	// Try and generate user creds for Aux cluster as ops user.
	_, err = helpers.GenerateUserCreds(helpers.UserCredsRequest{
		Process:        main.Process,
		Username:       mainOps,
		RouteToCluster: clusterAux,
	})
	require.True(t, trace.IsNotFound(err))

	// ListNodes expect labels as a value of host
	tc.Host = ""
	servers, err := tc.ListNodesWithFilters(ctx)
	require.NoError(t, err)
	require.Len(t, servers, 2)
	tc.Host = Loopback

	// check that remote cluster has been provisioned
	remoteClusters, err := main.Process.GetAuthServer().GetRemoteClusters()
	require.NoError(t, err)
	require.Len(t, remoteClusters, 1)
	require.Equal(t, clusterAux, remoteClusters[0].GetName())

	// after removing the remote cluster and trusted cluster, the connection will start failing
	require.NoError(t, main.Process.GetAuthServer().DeleteRemoteCluster(clusterAux))
	require.NoError(t, aux.Process.GetAuthServer().DeleteTrustedCluster(ctx, trustedCluster.GetName()))
	for i := 0; i < 10; i++ {
		time.Sleep(time.Millisecond * 50)
		err = tc.SSH(ctx, cmd, false)
		if err != nil {
			break
		}
	}
	require.Error(t, err, "expected tunnel to close and SSH client to start failing")

	// recreating the trusted cluster should re-establish connection
	_, err = aux.Process.GetAuthServer().UpsertTrustedCluster(ctx, trustedCluster)
	require.NoError(t, err)

	// check that remote cluster has been re-provisioned
	remoteClusters, err = main.Process.GetAuthServer().GetRemoteClusters()
	require.NoError(t, err)
	require.Len(t, remoteClusters, 1)
	require.Equal(t, clusterAux, remoteClusters[0].GetName())

	// Wait for both cluster to see each other via reverse tunnels.
	require.Eventually(t, helpers.WaitForClusters(main.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")

	// connection and client should recover and work again
	output = &bytes.Buffer{}
	tc.Stdout = output
	for i := 0; i < 10; i++ {
		time.Sleep(time.Millisecond * 50)
		err = tc.SSH(ctx, cmd, false)
		if err == nil {
			break
		}
	}
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output.String())

	// stop clusters and remaining nodes
	require.NoError(t, main.StopAll())
	require.NoError(t, aux.StopAll())
}

func testTrustedTunnelNode(t *testing.T, suite *integrationTestSuite) {
	ctx := context.Background()
	username := suite.Me.Username

	clusterMain := "cluster-main"
	clusterAux := "cluster-aux"
	main := suite.newNamedTeleportInstance(t, clusterMain)
	aux := suite.newNamedTeleportInstance(t, clusterAux)

	// main cluster has a local user and belongs to role "main-devs"
	mainDevs := "main-devs"
	role, err := types.NewRoleV3(mainDevs, types.RoleSpecV5{
		Allow: types.RoleConditions{
			Logins: []string{username},
		},
	})
	require.NoError(t, err)
	main.AddUserWithRole(username, role)

	// for role mapping test we turn on Web API on the main cluster
	// as it's used
	makeConfig := func(enableSSH bool) (*testing.T, []*helpers.InstanceSecrets, *service.Config) {
		tconf := suite.defaultServiceConfig()
		tconf.Proxy.DisableWebService = false
		tconf.Proxy.DisableWebInterface = true
		tconf.SSH.Enabled = enableSSH
		return t, nil, tconf
	}
	lib.SetInsecureDevMode(true)
	defer lib.SetInsecureDevMode(false)

	require.NoError(t, main.CreateEx(makeConfig(false)))
	require.NoError(t, aux.CreateEx(makeConfig(true)))

	// auxiliary cluster has a role aux-devs
	// connect aux cluster to main cluster
	// using trusted clusters, so remote user will be allowed to assume
	// role specified by mapping remote role "devs" to local role "local-devs"
	auxDevs := "aux-devs"
	role, err = types.NewRoleV3(auxDevs, types.RoleSpecV5{
		Allow: types.RoleConditions{
			Logins: []string{username},
		},
	})
	require.NoError(t, err)
	err = aux.Process.GetAuthServer().UpsertRole(ctx, role)
	require.NoError(t, err)
	trustedClusterToken := "trusted-cluster-token"
	err = main.Process.GetAuthServer().UpsertToken(ctx,
		services.MustCreateProvisionToken(trustedClusterToken, []types.SystemRole{types.RoleTrustedCluster}, time.Time{}))
	require.NoError(t, err)
	trustedCluster := main.AsTrustedCluster(trustedClusterToken, types.RoleMap{
		{Remote: mainDevs, Local: []string{auxDevs}},
	})

	// modify trusted cluster resource name, so it would not
	// match the cluster name to check that it does not matter
	trustedCluster.SetName(main.Secrets.SiteName + "-cluster")

	require.NoError(t, main.Start())
	require.NoError(t, aux.Start())

	err = trustedCluster.CheckAndSetDefaults()
	require.NoError(t, err)

	// try and upsert a trusted cluster
	helpers.TryCreateTrustedCluster(t, aux.Process.GetAuthServer(), trustedCluster)
	helpers.WaitForTunnelConnections(t, main.Process.GetAuthServer(), clusterAux, 1)

	// Create a Teleport instance with a node that dials back to the aux cluster.
	tunnelNodeHostname := "cluster-aux-node"
	nodeConfig := func() *service.Config {
		tconf := suite.defaultServiceConfig()
		tconf.Hostname = tunnelNodeHostname
		tconf.SetToken("token")
		tconf.SetAuthServerAddress(utils.NetAddr{
			AddrNetwork: "tcp",
			Addr:        aux.Web,
		})
		tconf.Auth.Enabled = false
		tconf.Proxy.Enabled = false
		tconf.SSH.Enabled = true
		return tconf
	}
	_, err = aux.StartNode(nodeConfig())
	require.NoError(t, err)

	// Wait for both cluster to see each other via reverse tunnels.
	require.Eventually(t, helpers.WaitForClusters(main.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")
	require.Eventually(t, helpers.WaitForClusters(aux.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")

	// Wait for both nodes to show up before attempting to dial to them.
	err = helpers.WaitForNodeCount(ctx, main, clusterAux, 2)
	require.NoError(t, err)

	cmd := []string{"echo", "hello world"}

	// Try and connect to a node in the Aux cluster from the Main cluster using
	// direct dialing.
	tc, err := main.NewClient(helpers.ClientConfig{
		Login:   username,
		Cluster: clusterAux,
		Host:    Loopback,
		Port:    helpers.Port(t, aux.SSH),
	})
	require.NoError(t, err)
	output := &bytes.Buffer{}
	tc.Stdout = output
	require.NoError(t, err)
	for i := 0; i < 10; i++ {
		time.Sleep(time.Millisecond * 50)
		err = tc.SSH(context.TODO(), cmd, false)
		if err == nil {
			break
		}
	}
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output.String())

	// Try and connect to a node in the Aux cluster from the Main cluster using
	// tunnel dialing.
	tunnelClient, err := main.NewClient(helpers.ClientConfig{
		Login:   username,
		Cluster: clusterAux,
		Host:    tunnelNodeHostname,
	})
	require.NoError(t, err)
	tunnelOutput := &bytes.Buffer{}
	tunnelClient.Stdout = tunnelOutput
	require.NoError(t, err)

	// Use assert package to get access to the returned error. In this way we can log it.
	if !assert.Eventually(t, func() bool {
		err = tunnelClient.SSH(context.Background(), cmd, false)
		return err == nil
	}, 10*time.Second, 200*time.Millisecond) {
		require.FailNow(t, "Failed to established SSH connection", err)
	}

	require.Equal(t, "hello world\n", tunnelOutput.String())

	// Stop clusters and remaining nodes.
	require.NoError(t, main.StopAll())
	require.NoError(t, aux.StopAll())
}

// TestDiscoveryRecovers ensures that discovery protocol recovers from a bad discovery
// state (all known proxies are offline).
func testDiscoveryRecovers(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	username := suite.Me.Username

	// create load balancer for main cluster proxies
	frontend := *utils.MustParseAddr(net.JoinHostPort(Loopback, "0"))
	lb, err := utils.NewLoadBalancer(context.TODO(), frontend)
	require.NoError(t, err)
	require.NoError(t, lb.Listen())
	go lb.Serve()
	defer lb.Close()

	remote := suite.newNamedTeleportInstance(t, "cluster-remote")
	main := suite.newNamedTeleportInstance(t, "cluster-main")

	remote.AddUser(username, []string{username})
	main.AddUser(username, []string{username})

	require.NoError(t, main.Create(t, remote.Secrets.AsSlice(), false, nil))
	mainSecrets := main.Secrets
	// switch listen address of the main cluster to load balancer
	mainProxyAddr := *utils.MustParseAddr(mainSecrets.TunnelAddr)
	lb.AddBackend(mainProxyAddr)
	mainSecrets.TunnelAddr = lb.Addr().String()
	require.NoError(t, remote.Create(t, mainSecrets.AsSlice(), true, nil))

	require.NoError(t, main.Start())
	require.NoError(t, remote.Start())

	// Wait for both cluster to see each other via reverse tunnels.
	require.Eventually(t, helpers.WaitForClusters(main.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")
	require.Eventually(t, helpers.WaitForClusters(remote.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")

	var reverseTunnelAddr string

	// Helper function for adding a new proxy to "main".
	addNewMainProxy := func(name string) (reversetunnel.Server, helpers.ProxyConfig) {
		t.Logf("adding main proxy %q...", name)
		newConfig := helpers.ProxyConfig{
			Name:              name,
			DisableWebService: true,
		}
		newConfig.SSHAddr = helpers.NewListenerOn(t, main.Hostname, service.ListenerNodeSSH, &newConfig.FileDescriptors)
		newConfig.WebAddr = helpers.NewListenerOn(t, main.Hostname, service.ListenerProxyWeb, &newConfig.FileDescriptors)
		newConfig.ReverseTunnelAddr = helpers.NewListenerOn(t, main.Hostname, service.ListenerProxyTunnel, &newConfig.FileDescriptors)
		reverseTunnelAddr = newConfig.ReverseTunnelAddr

		newProxy, _, err := main.StartProxy(newConfig)
		require.NoError(t, err)

		// add proxy as a backend to the load balancer
		lb.AddBackend(*utils.MustParseAddr(newConfig.ReverseTunnelAddr))
		return newProxy, newConfig
	}

	killMainProxy := func(name string) {
		t.Logf("killing main proxy %q...", name)
		for _, p := range main.Nodes {
			if !p.Config.Proxy.Enabled {
				continue
			}
			if p.Config.Hostname == name {
				require.NoError(t, lb.RemoveBackend(*utils.MustParseAddr(reverseTunnelAddr)))
				require.NoError(t, p.Close())
				require.NoError(t, p.Wait())
				return
			}
		}
		t.Errorf("cannot close proxy %q (not found)", name)
	}

	// Helper function for testing that a proxy in main has been discovered by
	// (and is able to use reverse tunnel into) remote.  If conf is nil, main's
	// first/default proxy will be called.
	testProxyConn := func(conf *helpers.ProxyConfig, shouldFail bool) {
		clientConf := helpers.ClientConfig{
			Login:   username,
			Cluster: "cluster-remote",
			Host:    Loopback,
			Port:    helpers.Port(t, remote.SSH),
			Proxy:   conf,
		}
		output, err := runCommand(t, main, []string{"echo", "hello world"}, clientConf, 10)
		if shouldFail {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, "hello world\n", output)
		}
	}

	// ensure that initial proxy's tunnel has been established
	helpers.WaitForActiveTunnelConnections(t, main.Tunnel, "cluster-remote", 1)
	// execute the connection via initial proxy; should not fail
	testProxyConn(nil, false)

	// helper funcion for making numbered proxy names
	pname := func(n int) string {
		return fmt.Sprintf("cluster-main-proxy-%d", n)
	}

	// create first numbered proxy
	_, c0 := addNewMainProxy(pname(0))
	// check that we now have two tunnel connections
	require.NoError(t, helpers.WaitForProxyCount(remote, "cluster-main", 2))
	// check that first numbered proxy is OK.
	testProxyConn(&c0, false)
	// remove the initial proxy.
	require.NoError(t, lb.RemoveBackend(mainProxyAddr))
	require.NoError(t, helpers.WaitForProxyCount(remote, "cluster-main", 1))

	// force bad state by iteratively removing previous proxy before
	// adding next proxy; this ensures that discovery protocol's list of
	// known proxies is all invalid.
	for i := 0; i < 6; i++ {
		prev, next := pname(i), pname(i+1)
		killMainProxy(prev)
		require.NoError(t, helpers.WaitForProxyCount(remote, "cluster-main", 0))
		_, cn := addNewMainProxy(next)
		require.NoError(t, helpers.WaitForProxyCount(remote, "cluster-main", 1))
		testProxyConn(&cn, false)
	}

	// Stop both clusters and remaining nodes.
	require.NoError(t, remote.StopAll())
	require.NoError(t, main.StopAll())
}

// TestDiscovery tests case for multiple proxies and a reverse tunnel
// agent that eventually connnects to the the right proxy
func testDiscovery(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	username := suite.Me.Username

	// create load balancer for main cluster proxies
	frontend := *utils.MustParseAddr(net.JoinHostPort(Loopback, "0"))
	lb, err := utils.NewLoadBalancer(context.TODO(), frontend)
	require.NoError(t, err)
	require.NoError(t, lb.Listen())
	go lb.Serve()
	defer lb.Close()

	remote := suite.newNamedTeleportInstance(t, "cluster-remote")
	main := suite.newNamedTeleportInstance(t, "cluster-main")

	remote.AddUser(username, []string{username})
	main.AddUser(username, []string{username})

	require.NoError(t, main.Create(t, remote.Secrets.AsSlice(), false, nil))
	mainSecrets := main.Secrets
	// switch listen address of the main cluster to load balancer
	mainProxyAddr := *utils.MustParseAddr(mainSecrets.TunnelAddr)
	lb.AddBackend(mainProxyAddr)
	mainSecrets.TunnelAddr = lb.Addr().String()
	require.NoError(t, remote.Create(t, mainSecrets.AsSlice(), true, nil))

	require.NoError(t, main.Start())
	require.NoError(t, remote.Start())

	// Wait for both cluster to see each other via reverse tunnels.
	require.Eventually(t, helpers.WaitForClusters(main.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")
	require.Eventually(t, helpers.WaitForClusters(remote.Tunnel, 1), 10*time.Second, 1*time.Second,
		"Two clusters do not see each other: tunnels are not working.")

	// start second proxy
	proxyConfig := helpers.ProxyConfig{
		Name:              "cluster-main-proxy",
		DisableWebService: true,
	}
	proxyConfig.SSHAddr = helpers.NewListenerOn(t, main.Hostname, service.ListenerNodeSSH, &proxyConfig.FileDescriptors)
	proxyConfig.WebAddr = helpers.NewListenerOn(t, main.Hostname, service.ListenerProxyWeb, &proxyConfig.FileDescriptors)
	proxyConfig.ReverseTunnelAddr = helpers.NewListenerOn(t, main.Hostname, service.ListenerProxyTunnel, &proxyConfig.FileDescriptors)

	secondProxy, _, err := main.StartProxy(proxyConfig)
	require.NoError(t, err)

	// add second proxy as a backend to the load balancer
	lb.AddBackend(*utils.MustParseAddr(proxyConfig.ReverseTunnelAddr))

	// At this point the main cluster should observe two tunnels
	// connected to it from remote cluster
	helpers.WaitForActiveTunnelConnections(t, main.Tunnel, "cluster-remote", 1)
	helpers.WaitForActiveTunnelConnections(t, secondProxy, "cluster-remote", 1)

	// execute the connection via first proxy
	cfg := helpers.ClientConfig{
		Login:   username,
		Cluster: "cluster-remote",
		Host:    Loopback,
		Port:    helpers.Port(t, remote.SSH),
	}
	output, err := runCommand(t, main, []string{"echo", "hello world"}, cfg, 1)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output)

	// Execute the connection via second proxy, should work. This command is
	// tried 10 times with 250 millisecond delay between each attempt to allow
	// the discovery request to be received and the connection added to the agent
	// pool.
	cfgProxy := helpers.ClientConfig{
		Login:   username,
		Cluster: "cluster-remote",
		Host:    Loopback,
		Port:    helpers.Port(t, remote.SSH),
		Proxy:   &proxyConfig,
	}
	output, err = runCommand(t, main, []string{"echo", "hello world"}, cfgProxy, 10)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output)

	// Now disconnect the main proxy and make sure it will reconnect eventually.
	require.NoError(t, lb.RemoveBackend(mainProxyAddr))
	helpers.WaitForActiveTunnelConnections(t, secondProxy, "cluster-remote", 1)

	// Requests going via main proxy should fail.
	_, err = runCommand(t, main, []string{"echo", "hello world"}, cfg, 1)
	require.Error(t, err)

	// Requests going via second proxy should succeed.
	output, err = runCommand(t, main, []string{"echo", "hello world"}, cfgProxy, 1)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output)

	// Connect the main proxy back and make sure agents have reconnected over time.
	// This command is tried 10 times with 250 millisecond delay between each
	// attempt to allow the discovery request to be received and the connection
	// added to the agent pool.
	lb.AddBackend(mainProxyAddr)

	// Once the proxy is added a matching tunnel connection should be created.
	helpers.WaitForActiveTunnelConnections(t, main.Tunnel, "cluster-remote", 1)
	helpers.WaitForActiveTunnelConnections(t, secondProxy, "cluster-remote", 1)

	// Requests going via main proxy should succeed.
	output, err = runCommand(t, main, []string{"echo", "hello world"}, cfg, 40)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output)

	// Stop one of proxies on the main cluster.
	err = main.StopProxy()
	require.NoError(t, err)

	// Wait for the remote cluster to detect the outbound connection is gone.
	require.NoError(t, helpers.WaitForProxyCount(remote, "cluster-main", 1))

	// Stop both clusters and remaining nodes.
	require.NoError(t, remote.StopAll())
	require.NoError(t, main.StopAll())
}

// TestReverseTunnelCollapse makes sure that when a reverse tunnel collapses
// nodes will reconnect when network connection between the proxy and node
// is restored.
func testReverseTunnelCollapse(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	t.Cleanup(func() { tr.Stop() })

	lib.SetInsecureDevMode(true)
	t.Cleanup(func() { lib.SetInsecureDevMode(false) })

	// Create and start load balancer for proxies.
	frontend := *utils.MustParseAddr(net.JoinHostPort(Loopback, "0"))
	lb, err := utils.NewLoadBalancer(context.TODO(), frontend)
	require.NoError(t, err)
	require.NoError(t, lb.Listen())
	go lb.Serve()
	t.Cleanup(func() { require.NoError(t, lb.Close()) })

	// Create a Teleport instance with Auth/Proxy.
	mainConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
		tconf := suite.defaultServiceConfig()

		tconf.Auth.Enabled = true

		tconf.Proxy.Enabled = true
		tconf.Proxy.TunnelPublicAddrs = []utils.NetAddr{
			{
				AddrNetwork: "tcp",
				Addr:        lb.Addr().String(),
			},
		}
		tconf.Proxy.DisableWebService = false
		tconf.Proxy.DisableWebInterface = true
		tconf.Proxy.DisableALPNSNIListener = true

		tconf.SSH.Enabled = false

		return t, nil, nil, tconf
	}
	main := suite.NewTeleportWithConfig(mainConfig())
	t.Cleanup(func() { require.NoError(t, main.StopAll()) })

	// Create a Teleport instance with a Proxy.
	proxyConfig := helpers.ProxyConfig{
		Name:                   "cluster-main-proxy",
		DisableWebService:      false,
		DisableWebInterface:    true,
		DisableALPNSNIListener: true,
	}
	proxyConfig.SSHAddr = helpers.NewListener(t, service.ListenerNodeSSH, &proxyConfig.FileDescriptors)
	proxyConfig.WebAddr = helpers.NewListener(t, service.ListenerProxyWeb, &proxyConfig.FileDescriptors)
	proxyConfig.ReverseTunnelAddr = helpers.NewListener(t, service.ListenerProxyTunnel, &proxyConfig.FileDescriptors)

	proxyTunnel, firstProxy, err := main.StartProxy(proxyConfig)
	require.NoError(t, err)

	// The Listener FDs injected into the first proxy instance will be closed
	// when that instance is stopped later in in the test, rendering them all
	// invalid. This will make the tunnel fail when it attempts to re-open once
	// a second proxy is started. We can't just inject a totally new listener
	// config into the second proxy when it starts, or the tunnel end points
	// won't be able to find it.
	//
	// The least bad option is to duplicate all of the first proxy's Listener
	// FDs and inject those duplicates prior to startiung the second proxy
	// instance.
	fdCache, err := firstProxy.ExportFileDescriptors()
	require.NoError(t, err)

	proxyOneBackend := utils.MustParseAddr(main.ReverseTunnel)
	lb.AddBackend(*proxyOneBackend)
	proxyTwoBackend := utils.MustParseAddr(proxyConfig.ReverseTunnelAddr)
	lb.AddBackend(*proxyTwoBackend)

	// Create a Teleport instance with a Node.
	nodeConfig := func() *service.Config {
		tconf := suite.defaultServiceConfig()
		tconf.Hostname = "cluster-main-node"
		tconf.SetToken("token")
		tconf.SetAuthServerAddress(utils.NetAddr{
			AddrNetwork: "tcp",
			Addr:        proxyConfig.WebAddr,
		})
		tconf.Auth.Enabled = false
		tconf.Proxy.Enabled = false
		tconf.SSH.Enabled = true

		return tconf
	}
	node, err := main.StartNode(nodeConfig())
	require.NoError(t, err)

	// Wait for active tunnel connections to be established.
	helpers.WaitForActiveTunnelConnections(t, main.Tunnel, helpers.Site, 0)
	helpers.WaitForActiveTunnelConnections(t, proxyTunnel, helpers.Site, 1)

	// Execute the connection via first proxy.
	cfg := helpers.ClientConfig{
		Login:   suite.Me.Username,
		Cluster: helpers.Site,
		Host:    "cluster-main-node",
	}
	_, err = runCommand(t, main, []string{"echo", "hello world"}, cfg, 1)
	require.Error(t, err)

	cfgProxy := helpers.ClientConfig{
		Login:   suite.Me.Username,
		Cluster: helpers.Site,
		Host:    "cluster-main-node",
		Proxy:   &proxyConfig,
	}

	output, err := runCommand(t, main, []string{"echo", "hello world"}, cfgProxy, 10)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output)

	// stop the proxy to collapse the tunnel
	require.NoError(t, main.StopProxy())
	helpers.WaitForActiveTunnelConnections(t, main.Tunnel, helpers.Site, 0)
	helpers.WaitForActiveTunnelConnections(t, proxyTunnel, helpers.Site, 0)

	// Requests going via both proxy will fail.
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer invoke(cancel)
	_, err = runCommandWithContext(timeoutCtx, t, main, []string{"echo", "hello world"}, cfg, 1)
	require.Error(t, err)

	timeoutCtx, cancel = context.WithTimeout(context.Background(), 1*time.Second)
	defer invoke(cancel)
	_, err = runCommandWithContext(timeoutCtx, t, main, []string{"echo", "hello world"}, cfgProxy, 1)
	require.Error(t, err)

	// wait for the node to reach a degraded state
	_, err = node.WaitForEventTimeout(5*time.Minute, service.TeleportDegradedEvent)
	require.NoError(t, err, "timed out waiting for node to become degraded")

	// start the proxy again and ensure the tunnel is re-established
	proxyConfig.FileDescriptors = fdCache
	proxyTunnel, _, err = main.StartProxy(proxyConfig)
	require.NoError(t, err)
	helpers.WaitForActiveTunnelConnections(t, main.Tunnel, helpers.Site, 0)
	helpers.WaitForActiveTunnelConnections(t, proxyTunnel, helpers.Site, 1)

	// Requests going to the connected proxy should succeed.
	_, err = runCommand(t, main, []string{"echo", "hello world"}, cfg, 1)
	require.Error(t, err)
	output, err = runCommand(t, main, []string{"echo", "hello world"}, cfgProxy, 40)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output)

	// Stop everything.
	err = proxyTunnel.Shutdown(context.Background())
	require.NoError(t, err)
}

// TestDiscoveryNode makes sure the discovery protocol works with nodes.
func testDiscoveryNode(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	lib.SetInsecureDevMode(true)
	defer lib.SetInsecureDevMode(false)

	// Create and start load balancer for proxies.
	frontend := *utils.MustParseAddr(net.JoinHostPort(Loopback, "0"))
	lb, err := utils.NewLoadBalancer(context.TODO(), frontend)
	require.NoError(t, err)
	err = lb.Listen()
	require.NoError(t, err)
	go lb.Serve()
	defer lb.Close()

	// Create a Teleport instance with Auth/Proxy.
	mainConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
		tconf := suite.defaultServiceConfig()

		tconf.Auth.Enabled = true

		tconf.Proxy.Enabled = true
		tconf.Proxy.TunnelPublicAddrs = []utils.NetAddr{
			{
				AddrNetwork: "tcp",
				Addr:        lb.Addr().String(),
			},
		}
		tconf.Proxy.DisableWebService = false
		tconf.Proxy.DisableWebInterface = true
		tconf.Proxy.DisableALPNSNIListener = true

		tconf.SSH.Enabled = false

		return t, nil, nil, tconf
	}
	main := suite.NewTeleportWithConfig(mainConfig())
	defer main.StopAll()

	// Create a Teleport instance with a Proxy.
	proxyConfig := helpers.ProxyConfig{
		Name:              "cluster-main-proxy",
		DisableWebService: true,
	}
	proxyConfig.SSHAddr = helpers.NewListenerOn(t, main.Hostname, service.ListenerNodeSSH, &proxyConfig.FileDescriptors)
	proxyConfig.WebAddr = helpers.NewListenerOn(t, main.Hostname, service.ListenerProxyWeb, &proxyConfig.FileDescriptors)
	proxyConfig.ReverseTunnelAddr = helpers.NewListenerOn(t, main.Hostname, service.ListenerProxyTunnel, &proxyConfig.FileDescriptors)

	proxyTunnel, _, err := main.StartProxy(proxyConfig)
	require.NoError(t, err)

	proxyOneBackend := utils.MustParseAddr(main.ReverseTunnel)
	lb.AddBackend(*proxyOneBackend)
	proxyTwoBackend := utils.MustParseAddr(proxyConfig.ReverseTunnelAddr)
	lb.AddBackend(*proxyTwoBackend)

	// Create a Teleport instance with a Node.
	nodeConfig := func() *service.Config {
		tconf := suite.defaultServiceConfig()
		tconf.Hostname = "cluster-main-node"
		tconf.SetToken("token")
		tconf.SetAuthServerAddress(utils.NetAddr{
			AddrNetwork: "tcp",
			Addr:        main.Web,
		})

		tconf.Auth.Enabled = false

		tconf.Proxy.Enabled = false

		tconf.SSH.Enabled = true

		return tconf
	}
	_, err = main.StartNode(nodeConfig())
	require.NoError(t, err)

	// Wait for active tunnel connections to be established.
	helpers.WaitForActiveTunnelConnections(t, main.Tunnel, helpers.Site, 1)
	helpers.WaitForActiveTunnelConnections(t, proxyTunnel, helpers.Site, 1)

	// Execute the connection via first proxy.
	cfg := helpers.ClientConfig{
		Login:   suite.Me.Username,
		Cluster: helpers.Site,
		Host:    "cluster-main-node",
	}
	output, err := runCommand(t, main, []string{"echo", "hello world"}, cfg, 1)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output)

	// Execute the connection via second proxy, should work. This command is
	// tried 10 times with 250 millisecond delay between each attempt to allow
	// the discovery request to be received and the connection added to the agent
	// pool.
	cfgProxy := helpers.ClientConfig{
		Login:   suite.Me.Username,
		Cluster: helpers.Site,
		Host:    "cluster-main-node",
		Proxy:   &proxyConfig,
	}

	output, err = runCommand(t, main, []string{"echo", "hello world"}, cfgProxy, 10)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output)

	// Remove second proxy from LB.
	require.NoError(t, lb.RemoveBackend(*proxyTwoBackend))
	helpers.WaitForActiveTunnelConnections(t, main.Tunnel, helpers.Site, 1)

	// Requests going via main proxy will succeed. Requests going via second
	// proxy will fail.
	output, err = runCommand(t, main, []string{"echo", "hello world"}, cfg, 1)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output)
	_, err = runCommand(t, main, []string{"echo", "hello world"}, cfgProxy, 1)
	require.Error(t, err)

	// Add second proxy to LB, both should have a connection.
	lb.AddBackend(*proxyTwoBackend)
	helpers.WaitForActiveTunnelConnections(t, main.Tunnel, helpers.Site, 1)
	helpers.WaitForActiveTunnelConnections(t, proxyTunnel, helpers.Site, 1)

	// Requests going via both proxies will succeed.
	output, err = runCommand(t, main, []string{"echo", "hello world"}, cfg, 1)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output)
	output, err = runCommand(t, main, []string{"echo", "hello world"}, cfgProxy, 40)
	require.NoError(t, err)
	require.Equal(t, "hello world\n", output)

	// Stop everything.
	err = proxyTunnel.Shutdown(context.Background())
	require.NoError(t, err)
	err = main.StopAll()
	require.NoError(t, err)
}

// TestExternalClient tests if we can connect to a node in a Teleport
// cluster. Both normal and recording proxies are tested.
func testExternalClient(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// Only run this test if we have access to the external SSH binary.
	_, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("Skipping TestExternalClient, no external SSH binary found.")
		return
	}

	tests := []struct {
		desc             string
		inRecordLocation string
		inForwardAgent   bool
		inCommand        string
		outError         bool
		outExecOutput    string
	}{
		// Record at the node, forward agent. Will still work even though the agent
		// will be rejected by the proxy (agent forwarding request rejection is a
		// soft failure).
		{
			desc:             "Record at Node with Agent Forwarding",
			inRecordLocation: types.RecordAtNode,
			inForwardAgent:   true,
			inCommand:        "echo hello",
			outError:         false,
			outExecOutput:    "hello",
		},
		// Record at the node, don't forward agent, will work. This is the normal
		// Teleport mode of operation.
		{
			desc:             "Record at Node without Agent Forwarding",
			inRecordLocation: types.RecordAtNode,
			inForwardAgent:   false,
			inCommand:        "echo hello",
			outError:         false,
			outExecOutput:    "hello",
		},
		// Record at the proxy, forward agent. Will work.
		{
			desc:             "Record at Proxy with Agent Forwarding",
			inRecordLocation: types.RecordAtProxy,
			inForwardAgent:   true,
			inCommand:        "echo hello",
			outError:         false,
			outExecOutput:    "hello",
		},
		// Record at the proxy, don't forward agent, request will fail because
		// recording proxy requires an agent.
		{
			desc:             "Record at Proxy without Agent Forwarding",
			inRecordLocation: types.RecordAtProxy,
			inForwardAgent:   false,
			inCommand:        "echo hello",
			outError:         true,
			outExecOutput:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			// Create a Teleport instance with auth, proxy, and node.
			makeConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
				recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
					Mode: tt.inRecordLocation,
				})
				require.NoError(t, err)

				tconf := suite.defaultServiceConfig()
				tconf.Auth.Enabled = true
				tconf.Auth.SessionRecordingConfig = recConfig

				tconf.Proxy.Enabled = true
				tconf.Proxy.DisableWebService = true
				tconf.Proxy.DisableWebInterface = true

				tconf.SSH.Enabled = true

				return t, nil, nil, tconf
			}
			teleport := suite.NewTeleportWithConfig(makeConfig())
			defer teleport.StopAll()

			// Generate certificates for the user simulating login.
			creds, err := helpers.GenerateUserCreds(helpers.UserCredsRequest{
				Process:  teleport.Process,
				Username: suite.Me.Username,
			})
			require.NoError(t, err)

			// Start (and defer close) a agent that runs during this integration test.
			teleAgent, socketDirPath, socketPath, err := helpers.CreateAgent(suite.Me, &creds.Key)
			require.NoError(t, err)
			defer helpers.CloseAgent(teleAgent, socketDirPath)

			// Create a *exec.Cmd that will execute the external SSH command.
			execCmd, err := helpers.ExternalSSHCommand(helpers.CommandOptions{
				ForwardAgent: tt.inForwardAgent,
				SocketPath:   socketPath,
				ProxyPort:    helpers.PortStr(t, teleport.SSHProxy),
				NodePort:     helpers.PortStr(t, teleport.SSH),
				Command:      tt.inCommand,
			})
			require.NoError(t, err)

			// Execute SSH command and check the output is what we expect.
			output, err := execCmd.Output()
			if tt.outError {
				require.Error(t, err)
			} else {
				if err != nil {
					// If an *exec.ExitError is returned, parse it and return stderr. If this
					// is not done then c.Assert will just print a byte array for the error.
					er, ok := err.(*exec.ExitError)
					if ok {
						t.Fatalf("Unexpected error: %v", string(er.Stderr))
					}
				}
				require.NoError(t, err)
				require.Equal(t, tt.outExecOutput, strings.TrimSpace(string(output)))
			}
		})
	}
}

// TestControlMaster checks if multiple SSH channels can be created over the
// same connection. This is frequently used by tools like Ansible.
func testControlMaster(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// Only run this test if we have access to the external SSH binary.
	_, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("Skipping TestControlMaster, no external SSH binary found.")
		return
	}

	tests := []struct {
		inRecordLocation string
	}{
		// Run tests when Teleport is recording sessions at the node.
		{
			inRecordLocation: types.RecordAtNode,
		},
		// Run tests when Teleport is recording sessions at the proxy
		// (temporarily disabled, see https://github.com/gravitational/teleport/issues/16224)
		// {
		// 	inRecordLocation: types.RecordAtProxy,
		// },
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("recording_mode=%s", tt.inRecordLocation), func(t *testing.T) {
			controlDir, err := os.MkdirTemp("", "teleport-")
			require.NoError(t, err)
			defer os.RemoveAll(controlDir)
			controlPath := filepath.Join(controlDir, "control-path")

			// Create a Teleport instance with auth, proxy, and node.
			makeConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
				recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
					Mode: tt.inRecordLocation,
				})
				require.NoError(t, err)

				tconf := suite.defaultServiceConfig()
				tconf.Auth.Enabled = true
				tconf.Auth.SessionRecordingConfig = recConfig

				tconf.Proxy.Enabled = true
				tconf.Proxy.DisableWebService = true
				tconf.Proxy.DisableWebInterface = true

				tconf.SSH.Enabled = true

				return t, nil, nil, tconf
			}
			teleport := suite.NewTeleportWithConfig(makeConfig())
			defer teleport.StopAll()

			// Generate certificates for the user simulating login.
			creds, err := helpers.GenerateUserCreds(helpers.UserCredsRequest{
				Process:  teleport.Process,
				Username: suite.Me.Username,
			})
			require.NoError(t, err)

			// Start (and defer close) a agent that runs during this integration test.
			teleAgent, socketDirPath, socketPath, err := helpers.CreateAgent(suite.Me, &creds.Key)
			require.NoError(t, err)
			defer helpers.CloseAgent(teleAgent, socketDirPath)

			// Create and run an exec command twice with the passed in ControlPath. This
			// will cause re-use of the connection and creation of two sessions within
			// the connection.
			for i := 0; i < 2; i++ {
				execCmd, err := helpers.ExternalSSHCommand(helpers.CommandOptions{
					ForcePTY:     true,
					ForwardAgent: true,
					ControlPath:  controlPath,
					SocketPath:   socketPath,
					ProxyPort:    helpers.PortStr(t, teleport.SSHProxy),
					NodePort:     helpers.PortStr(t, teleport.SSH),
					Command:      "echo hello",
				})
				require.NoError(t, err)

				// Execute SSH command and check the output is what we expect.
				output, err := execCmd.Output()
				if err != nil {
					// If an *exec.ExitError is returned, parse it and return stderr. If this
					// is not done then c.Assert will just print a byte array for the error.
					er, ok := err.(*exec.ExitError)
					if ok {
						t.Fatalf("Unexpected error: %v", string(er.Stderr))
					}
				}
				require.NoError(t, err)
				require.True(t, strings.HasSuffix(strings.TrimSpace(string(output)), "hello"))
			}
		})
	}
}

// testProxyHostKeyCheck uses the forwarding proxy to connect to a server that
// presents a host key instead of a certificate in different configurations
// for the host key checking parameter in services.ClusterConfig.
func testProxyHostKeyCheck(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	tests := []struct {
		desc           string
		inHostKeyCheck bool
		outError       bool
	}{
		// disable host key checking, should be able to connect
		{
			desc:           "Disabled",
			inHostKeyCheck: false,
			outError:       false,
		},
		// enable host key checking, should NOT be able to connect
		{
			desc:           "Enabled",
			inHostKeyCheck: true,
			outError:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			hostSigner, err := ssh.ParsePrivateKey(suite.Priv)
			require.NoError(t, err)

			// start a ssh server that presents a host key instead of a certificate
			nodePort := newPortValue()
			sshNode, err := helpers.NewDiscardServer(Host, nodePort, hostSigner)
			require.NoError(t, err)
			err = sshNode.Start()
			require.NoError(t, err)
			defer sshNode.Stop()

			// create a teleport instance with auth, proxy, and node
			makeConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
				recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
					Mode:                types.RecordAtProxy,
					ProxyChecksHostKeys: types.NewBoolOption(tt.inHostKeyCheck),
				})
				require.NoError(t, err)

				tconf := suite.defaultServiceConfig()
				tconf.Auth.Enabled = true
				tconf.Auth.SessionRecordingConfig = recConfig

				tconf.Proxy.Enabled = true
				tconf.Proxy.DisableWebService = true
				tconf.Proxy.DisableWebInterface = true

				return t, nil, nil, tconf
			}
			teleport := suite.NewTeleportWithConfig(makeConfig())
			defer teleport.StopAll()

			// create a teleport client and exec a command
			clientConfig := helpers.ClientConfig{
				Login:        suite.Me.Username,
				Cluster:      helpers.Site,
				Host:         Host,
				Port:         nodePort,
				ForwardAgent: true,
			}
			_, err = runCommand(t, teleport, []string{"echo hello"}, clientConfig, 1)

			// check if we were able to exec the command or not
			if tt.outError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// testAuditOff checks that when session recording has been turned off,
// sessions are not recorded.
func testAuditOff(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()
	ctx := context.Background()

	var err error

	// create a teleport instance with auth, proxy, and node
	makeConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
		recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
			Mode: types.RecordOff,
		})
		require.NoError(t, err)

		tconf := suite.defaultServiceConfig()
		tconf.Auth.Enabled = true
		tconf.Auth.SessionRecordingConfig = recConfig

		tconf.Proxy.Enabled = true
		tconf.Proxy.DisableWebService = true
		tconf.Proxy.DisableWebInterface = true

		tconf.SSH.Enabled = true

		return t, nil, nil, tconf
	}
	teleport := suite.NewTeleportWithConfig(makeConfig())
	defer teleport.StopAll()

	// get access to a authClient for the cluster
	site := teleport.GetSiteAPI(helpers.Site)
	require.NotNil(t, site)

	// should have no sessions in it to start with
	sessions, _ := site.GetActiveSessionTrackers(ctx)
	require.Len(t, sessions, 0)

	// create interactive session (this goroutine is this user's terminal time)
	endCh := make(chan error, 1)

	myTerm := NewTerminal(250)
	go func() {
		cl, err := teleport.NewClient(helpers.ClientConfig{
			Login:   suite.Me.Username,
			Cluster: helpers.Site,
			Host:    Host,
			Port:    helpers.Port(t, teleport.SSH),
		})
		if err != nil {
			endCh <- err
			return
		}
		cl.Stdout = myTerm
		cl.Stdin = myTerm
		err = cl.SSH(ctx, []string{}, false)
		endCh <- err
	}()

	// wait until there's a session in there:
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	sessions, err = waitForSessionToBeEstablished(timeoutCtx, defaults.Namespace, site)
	require.NoError(t, err)
	tracker := sessions[0]

	// wait for the user to join this session
	for len(tracker.GetParticipants()) == 0 {
		time.Sleep(time.Millisecond * 5)
		tracker, err = site.GetSessionTracker(ctx, sessions[0].GetSessionID())
		require.NoError(t, err)
	}
	// make sure it's us who joined! :)
	require.Equal(t, suite.Me.Username, tracker.GetParticipants()[0].User)

	// lets type "echo hi" followed by "enter" and then "exit" + "enter":
	myTerm.Type("\aecho hi\n\r\aexit\n\r\a")

	// wait for session to end
	select {
	case <-time.After(1 * time.Minute):
		t.Fatalf("Timed out waiting for session to end.")
	case err := <-endCh:
		require.NoError(t, err)
	}

	// audit log should have the fact that the session occurred recorded in it
	// but the session could have been garbage collected at this point.

	// however, attempts to read the actual sessions should fail because it was
	// not actually recorded
	_, err = site.GetSessionChunk(apidefaults.Namespace, session.ID(tracker.GetSessionID()), 0, events.MaxChunkBytes)
	require.Error(t, err)
}

// testPAM checks that Teleport PAM integration works correctly. In this case
// that means if the account and session modules return success, the user
// should be allowed to log in. If either the account or session module does
// not return success, the user should not be able to log in.
func testPAM(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// Check if TestPAM can run. For PAM tests to run, the binary must have
	// been built with PAM support and the system running the tests must have
	// libpam installed, and have the policy files installed. This test is
	// always run in a container as part of the CI/CD pipeline. To run this
	// test locally, install the pam_teleport.so module by running 'sudo make
	// install' from the build.assets/pam/ directory. This will install the PAM
	// module as well as the policy files.
	if !pam.BuildHasPAM() || !pam.SystemHasPAM() || !hasPAMPolicy() {
		skipMessage := "Skipping TestPAM: no policy found. To run PAM tests run " +
			"'sudo make install' from the build.assets/pam/ directory."
		t.Skip(skipMessage)
	}

	tests := []struct {
		desc          string
		inEnabled     bool
		inServiceName string
		inUsePAMAuth  bool
		outContains   []string
		environment   map[string]string
	}{
		// 0 - No PAM support, session should work but no PAM related output.
		{
			desc:          "Disabled",
			inEnabled:     false,
			inServiceName: "",
			inUsePAMAuth:  true,
			outContains:   []string{},
		},
		// 1 - PAM enabled, module account and session functions return success.
		{
			desc:          "Enabled with Module Account & Session functions succeeding",
			inEnabled:     true,
			inServiceName: "teleport-success",
			inUsePAMAuth:  true,
			outContains: []string{
				"pam_sm_acct_mgmt OK",
				"pam_sm_authenticate OK",
				"pam_sm_open_session OK",
				"pam_sm_close_session OK",
			},
		},
		// 2 - PAM enabled, module account and session functions return success.
		{
			desc:          "Enabled with Module & Session functions succeeding",
			inEnabled:     true,
			inServiceName: "teleport-success",
			inUsePAMAuth:  false,
			outContains: []string{
				"pam_sm_acct_mgmt OK",
				"pam_sm_open_session OK",
				"pam_sm_close_session OK",
			},
		},
		// 3 - PAM enabled, module account functions fail.
		{
			desc:          "Enabled with all functions failing",
			inEnabled:     true,
			inServiceName: "teleport-acct-failure",
			inUsePAMAuth:  true,
			outContains:   []string{},
		},
		// 4 - PAM enabled, module session functions fail.
		{
			desc:          "Enabled with Module & Session functions failing",
			inEnabled:     true,
			inServiceName: "teleport-session-failure",
			inUsePAMAuth:  true,
			outContains:   []string{},
		},
		// 5 - PAM enabled, custom environment variables are passed.
		{
			desc:          "Enabled with custom environment",
			inEnabled:     true,
			inServiceName: "teleport-custom-env",
			inUsePAMAuth:  false,
			outContains: []string{
				"pam_sm_acct_mgmt OK",
				"pam_sm_open_session OK",
				"pam_sm_close_session OK",
				"pam_custom_envs OK",
			},
			environment: map[string]string{
				"FIRST_NAME": "JOHN",
				"LAST_NAME":  "DOE",
				"OTHER":      "{{ external.testing }}",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			// Create a teleport instance with auth, proxy, and node.
			makeConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
				tconf := suite.defaultServiceConfig()
				tconf.Auth.Enabled = true

				tconf.Proxy.Enabled = true
				tconf.Proxy.DisableWebService = true
				tconf.Proxy.DisableWebInterface = true

				tconf.SSH.Enabled = true
				tconf.SSH.PAM.Enabled = tt.inEnabled
				tconf.SSH.PAM.ServiceName = tt.inServiceName
				tconf.SSH.PAM.UsePAMAuth = tt.inUsePAMAuth
				tconf.SSH.PAM.Environment = tt.environment

				return t, nil, nil, tconf
			}
			teleport := suite.NewTeleportWithConfig(makeConfig())
			defer teleport.StopAll()

			termSession := NewTerminal(250)

			errCh := make(chan error)

			// Create an interactive session and write something to the terminal.
			go func() {
				cl, err := teleport.NewClient(helpers.ClientConfig{
					Login:   suite.Me.Username,
					Cluster: helpers.Site,
					Host:    Host,
					Port:    helpers.Port(t, teleport.SSH),
				})
				if err != nil {
					errCh <- err
					return
				}

				cl.Stdout = termSession
				cl.Stdin = termSession

				termSession.Type("\aecho hi\n\r\aexit\n\r\a")
				err = cl.SSH(context.TODO(), []string{}, false)
				if !isSSHError(err) {
					errCh <- err
					return
				}
				errCh <- nil
			}()

			// Wait for the session to end or timeout after 10 seconds.
			select {
			case <-time.After(10 * time.Second):
				dumpGoroutineProfile()
				t.Fatalf("Timeout exceeded waiting for session to complete.")
			case err := <-errCh:
				require.NoError(t, err)
			}

			// If any output is expected, check to make sure it was output.
			if len(tt.outContains) > 0 {
				for _, expectedOutput := range tt.outContains {
					output := termSession.Output(1024)
					t.Logf("got output: %q; want output to contain: %q", output, expectedOutput)
					require.Contains(t, output, expectedOutput)
				}
			}
		})
	}
}

// testRotateSuccess tests full cycle cert authority rotation
func testRotateSuccess(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	teleport := suite.NewTeleportInstance(t)
	defer teleport.StopAll()

	logins := []string{suite.Me.Username}
	for _, login := range logins {
		teleport.AddUser(login, []string{login})
	}

	tconf := suite.rotationConfig(true)
	config, err := teleport.GenerateConfig(t, nil, tconf)
	require.NoError(t, err)

	// Enable Kubernetes/Desktop services to test that the ready event is propagated.
	helpers.EnableKubernetesService(t, config)
	helpers.EnableDesktopService(config)

	serviceC := make(chan *service.TeleportProcess, 20)

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- service.Run(ctx, *config, func(cfg *service.Config) (service.Process, error) {
			svc, err := service.NewTeleport(cfg, service.WithIMDSClient(&helpers.DisabledIMDSClient{}))
			if err == nil {
				serviceC <- svc
			}
			return svc, err
		})
	}()

	svc, err := waitForProcessStart(serviceC)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		_, err := svc.GetIdentity(types.RoleNode)
		return err == nil
	}, 5*time.Second, 500*time.Millisecond)
	checkSSHPrincipals := func(svc *service.TeleportProcess) {
		id, err := svc.GetIdentity(types.RoleNode)
		require.NoError(t, err)
		require.Contains(t, id.Cert.ValidPrincipals, svc.Config.Hostname)
		require.Contains(t, id.Cert.ValidPrincipals, svc.Config.Hostname+"."+helpers.Site)
		require.Contains(t, id.Cert.ValidPrincipals, helpers.HostID)
		require.Contains(t, id.Cert.ValidPrincipals, helpers.HostID+"."+helpers.Site)
	}
	checkSSHPrincipals(svc)

	// Setup user in the cluster
	err = helpers.SetupUser(svc, suite.Me.Username, nil)
	require.NoError(t, err)

	// capture credentials before reload started to simulate old client
	initialCreds, err := helpers.GenerateUserCreds(helpers.UserCredsRequest{Process: svc, Username: suite.Me.Username})
	require.NoError(t, err)

	t.Logf("Service started. Setting rotation state to %v", types.RotationPhaseUpdateClients)

	// start rotation
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseInit,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	hostCA, err := svc.GetAuthServer().GetCertAuthority(ctx, types.CertAuthID{Type: types.HostCA, DomainName: helpers.Site}, false)
	require.NoError(t, err)
	t.Logf("Cert authority: %v", auth.CertAuthorityInfo(hostCA))

	// wait until service phase update to be broadcasted (init phase does not trigger reload)
	err = waitForProcessEvent(svc, service.TeleportPhaseChangeEvent, 10*time.Second)
	require.NoError(t, err)

	// update clients
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseUpdateClients,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	// wait until service reload
	svc, err = waitForReload(serviceC, svc)
	require.NoError(t, err)

	cfg := helpers.ClientConfig{
		Login:   suite.Me.Username,
		Cluster: helpers.Site,
		Host:    Loopback,
		Port:    helpers.Port(t, teleport.SSH),
	}
	clt, err := teleport.NewClientWithCreds(cfg, *initialCreds)
	require.NoError(t, err)

	// client works as is before servers have been rotated
	err = runAndMatch(clt, 8, []string{"echo", "hello world"}, ".*hello world.*")
	require.NoError(t, err)
	checkSSHPrincipals(svc)

	t.Logf("Service reloaded. Setting rotation state to %v", types.RotationPhaseUpdateServers)

	// move to the next phase
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseUpdateServers,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	hostCA, err = svc.GetAuthServer().GetCertAuthority(ctx, types.CertAuthID{Type: types.HostCA, DomainName: helpers.Site}, false)
	require.NoError(t, err)
	t.Logf("Cert authority: %v", auth.CertAuthorityInfo(hostCA))

	// wait until service reloaded
	svc, err = waitForReload(serviceC, svc)
	require.NoError(t, err)

	// new credentials will work from this phase to others
	newCreds, err := helpers.GenerateUserCreds(helpers.UserCredsRequest{Process: svc, Username: suite.Me.Username})
	require.NoError(t, err)

	clt, err = teleport.NewClientWithCreds(cfg, *newCreds)
	require.NoError(t, err)

	// new client works
	err = runAndMatch(clt, 8, []string{"echo", "hello world"}, ".*hello world.*")
	require.NoError(t, err)
	checkSSHPrincipals(svc)

	t.Logf("Service reloaded. Setting rotation state to %v.", types.RotationPhaseStandby)

	// complete rotation
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseStandby,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	hostCA, err = svc.GetAuthServer().GetCertAuthority(ctx, types.CertAuthID{Type: types.HostCA, DomainName: helpers.Site}, false)
	require.NoError(t, err)
	t.Logf("Cert authority: %v", auth.CertAuthorityInfo(hostCA))

	// wait until service reloaded
	svc, err = waitForReload(serviceC, svc)
	require.NoError(t, err)

	// new client still works
	err = runAndMatch(clt, 8, []string{"echo", "hello world"}, ".*hello world.*")
	require.NoError(t, err)
	checkSSHPrincipals(svc)

	t.Logf("Service reloaded. Rotation has completed. Shutting down service.")

	// shut down the service
	cancel()
	// close the service without waiting for the connections to drain
	require.NoError(t, svc.Close())

	select {
	case err := <-runErrCh:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatalf("failed to shut down the server")
	}
}

// TestRotateRollback tests cert authority rollback
func testRotateRollback(t *testing.T, s *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tconf := s.rotationConfig(true)
	teleport := s.NewTeleportInstance(t)
	defer teleport.StopAll()
	logins := []string{s.Me.Username}
	for _, login := range logins {
		teleport.AddUser(login, []string{login})
	}
	config, err := teleport.GenerateConfig(t, nil, tconf)
	require.NoError(t, err)

	serviceC := make(chan *service.TeleportProcess, 20)

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- service.Run(ctx, *config, func(cfg *service.Config) (service.Process, error) {
			svc, err := service.NewTeleport(cfg, service.WithIMDSClient(&helpers.DisabledIMDSClient{}))
			if err == nil {
				serviceC <- svc
			}
			return svc, err
		})
	}()

	svc, err := waitForProcessStart(serviceC)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		_, err := svc.GetIdentity(types.RoleNode)
		return err == nil
	}, 5*time.Second, 500*time.Millisecond)
	checkSSHPrincipals := func(svc *service.TeleportProcess) {
		id, err := svc.GetIdentity(types.RoleNode)
		require.NoError(t, err)
		require.Contains(t, id.Cert.ValidPrincipals, svc.Config.Hostname)
		require.Contains(t, id.Cert.ValidPrincipals, svc.Config.Hostname+"."+helpers.Site)
		require.Contains(t, id.Cert.ValidPrincipals, helpers.HostID)
		require.Contains(t, id.Cert.ValidPrincipals, helpers.HostID+"."+helpers.Site)
	}
	checkSSHPrincipals(svc)

	// Setup user in the cluster
	err = helpers.SetupUser(svc, s.Me.Username, nil)
	require.NoError(t, err)

	// capture credentials before reload started to simulate old client
	initialCreds, err := helpers.GenerateUserCreds(helpers.UserCredsRequest{Process: svc, Username: s.Me.Username})
	require.NoError(t, err)

	t.Logf("Service started. Setting rotation state to %q.", types.RotationPhaseInit)

	// start rotation
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseInit,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	err = waitForProcessEvent(svc, service.TeleportPhaseChangeEvent, 10*time.Second)
	require.NoError(t, err)

	t.Logf("Setting rotation state to %q.", types.RotationPhaseUpdateClients)

	// start rotation
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseUpdateClients,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	// wait until service reload
	svc, err = waitForReload(serviceC, svc)
	require.NoError(t, err)

	cfg := helpers.ClientConfig{
		Login:   s.Me.Username,
		Cluster: helpers.Site,
		Host:    Loopback,
		Port:    helpers.Port(t, teleport.SSH),
	}
	clt, err := teleport.NewClientWithCreds(cfg, *initialCreds)
	require.NoError(t, err)

	// client works as is before servers have been rotated
	err = runAndMatch(clt, 8, []string{"echo", "hello world"}, ".*hello world.*")
	require.NoError(t, err)
	checkSSHPrincipals(svc)

	t.Logf("Service reloaded. Setting rotation state to %q.", types.RotationPhaseUpdateServers)

	// move to the next phase
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseUpdateServers,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	// wait until service reloaded
	svc, err = waitForReload(serviceC, svc)
	require.NoError(t, err)

	t.Logf("Service reloaded. Setting rotation state to %q.", types.RotationPhaseRollback)

	// complete rotation
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseRollback,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	// wait until service reloaded
	svc, err = waitForReload(serviceC, svc)
	require.NoError(t, err)

	// old client works
	err = runAndMatch(clt, 8, []string{"echo", "hello world"}, ".*hello world.*")
	require.NoError(t, err)
	checkSSHPrincipals(svc)

	t.Log("Service reloaded. Rotation has completed. Shutting down service.")

	// shut down the service
	cancel()
	// close the service without waiting for the connections to drain
	svc.Close()

	select {
	case err := <-runErrCh:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatalf("failed to shut down the server")
	}
}

// TestRotateTrustedClusters tests CA rotation support for trusted clusters
func testRotateTrustedClusters(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	t.Cleanup(func() { tr.Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	clusterMain := "rotate-main"
	clusterAux := "rotate-aux"

	tconf := suite.rotationConfig(false)
	main := suite.newNamedTeleportInstance(t, clusterMain)
	aux := suite.newNamedTeleportInstance(t, clusterAux)

	logins := []string{suite.Me.Username}
	for _, login := range logins {
		main.AddUser(login, []string{login})
	}
	config, err := main.GenerateConfig(t, nil, tconf)
	require.NoError(t, err)

	serviceC := make(chan *service.TeleportProcess, 20)
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- service.Run(ctx, *config, func(cfg *service.Config) (service.Process, error) {
			svc, err := service.NewTeleport(cfg, service.WithIMDSClient(&helpers.DisabledIMDSClient{}))
			if err == nil {
				serviceC <- svc
			}
			return svc, err
		})
	}()

	svc, err := waitForProcessStart(serviceC)
	require.NoError(t, err)

	// main cluster has a local user and belongs to role "main-devs"
	mainDevs := "main-devs"
	role, err := types.NewRoleV3(mainDevs, types.RoleSpecV5{
		Allow: types.RoleConditions{
			Logins: []string{suite.Me.Username},
		},
	})
	require.NoError(t, err)

	err = helpers.SetupUser(svc, suite.Me.Username, []types.Role{role})
	require.NoError(t, err)

	// create auxiliary cluster and setup trust
	require.NoError(t, aux.CreateEx(t, nil, suite.rotationConfig(false)))

	// auxiliary cluster has a role aux-devs
	// connect aux cluster to main cluster
	// using trusted clusters, so remote user will be allowed to assume
	// role specified by mapping remote role "devs" to local role "local-devs"
	auxDevs := "aux-devs"
	role, err = types.NewRoleV3(auxDevs, types.RoleSpecV5{
		Allow: types.RoleConditions{
			Logins: []string{suite.Me.Username},
		},
	})
	require.NoError(t, err)
	err = aux.Process.GetAuthServer().UpsertRole(ctx, role)
	require.NoError(t, err)
	trustedClusterToken := "trusted-cluster-token"
	err = svc.GetAuthServer().UpsertToken(ctx,
		services.MustCreateProvisionToken(trustedClusterToken, []types.SystemRole{types.RoleTrustedCluster}, time.Time{}))
	require.NoError(t, err)
	trustedCluster := main.AsTrustedCluster(trustedClusterToken, types.RoleMap{
		{Remote: mainDevs, Local: []string{auxDevs}},
	})
	require.NoError(t, aux.Start())

	// try and upsert a trusted cluster
	lib.SetInsecureDevMode(true)
	defer lib.SetInsecureDevMode(false)

	helpers.TryCreateTrustedCluster(t, aux.Process.GetAuthServer(), trustedCluster)
	helpers.WaitForTunnelConnections(t, svc.GetAuthServer(), aux.Secrets.SiteName, 1)

	// capture credentials before reload has started to simulate old client
	initialCreds, err := helpers.GenerateUserCreds(helpers.UserCredsRequest{
		Process:  svc,
		Username: suite.Me.Username,
	})
	require.NoError(t, err)

	// credentials should work
	cfg := helpers.ClientConfig{
		Login:   suite.Me.Username,
		Host:    Loopback,
		Cluster: clusterAux,
		Port:    helpers.Port(t, aux.SSH),
	}
	clt, err := main.NewClientWithCreds(cfg, *initialCreds)
	require.NoError(t, err)

	err = runAndMatch(clt, 8, []string{"echo", "hello world"}, ".*hello world.*")
	require.NoError(t, err)

	t.Logf("Setting rotation state to %v", types.RotationPhaseInit)

	// start rotation
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseInit,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	// wait until service phase update to be broadcast (init phase does not trigger reload)
	err = waitForProcessEvent(svc, service.TeleportPhaseChangeEvent, 10*time.Second)
	require.NoError(t, err)

	// waitForPhase waits until aux cluster detects the rotation
	waitForPhase := func(phase string) {
		require.Eventually(t, func() bool {
			ca, err := aux.Process.GetAuthServer().GetCertAuthority(
				ctx,
				types.CertAuthID{
					Type:       types.HostCA,
					DomainName: clusterMain,
				}, false)
			if err != nil {
				return false
			}

			if ca.GetRotation().Phase == phase {
				return true
			}

			return false
		}, 30*time.Second, 250*time.Millisecond, "failed to converge to phase %q", phase)
	}

	waitForPhase(types.RotationPhaseInit)

	// update clients
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseUpdateClients,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	// wait until service reloaded
	svc, err = waitForReload(serviceC, svc)
	require.NoError(t, err)

	waitForPhase(types.RotationPhaseUpdateClients)

	// old client should work as is
	err = runAndMatch(clt, 8, []string{"echo", "hello world"}, ".*hello world.*")
	require.NoError(t, err)

	t.Logf("Service reloaded. Setting rotation state to %v", types.RotationPhaseUpdateServers)

	// move to the next phase
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseUpdateServers,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	// wait until service reloaded
	svc, err = waitForReload(serviceC, svc)
	require.NoError(t, err)

	waitForPhase(types.RotationPhaseUpdateServers)

	// new credentials will work from this phase to others
	newCreds, err := helpers.GenerateUserCreds(helpers.UserCredsRequest{Process: svc, Username: suite.Me.Username})
	require.NoError(t, err)

	clt, err = main.NewClientWithCreds(cfg, *newCreds)
	require.NoError(t, err)

	// new client works
	err = runAndMatch(clt, 8, []string{"echo", "hello world"}, ".*hello world.*")
	require.NoError(t, err)

	t.Logf("Service reloaded. Setting rotation state to %v.", types.RotationPhaseStandby)

	// complete rotation
	err = svc.GetAuthServer().RotateCertAuthority(ctx, auth.RotateRequest{
		TargetPhase: types.RotationPhaseStandby,
		Mode:        types.RotationModeManual,
	})
	require.NoError(t, err)

	// wait until service reloaded
	t.Log("Waiting for service reload.")
	svc, err = waitForReload(serviceC, svc)
	require.NoError(t, err)
	t.Log("Service reload completed, waiting for phase.")

	waitForPhase(types.RotationPhaseStandby)
	t.Log("Phase completed.")

	// new client still works
	err = runAndMatch(clt, 8, []string{"echo", "hello world"}, ".*hello world.*")
	require.NoError(t, err)

	t.Log("Service reloaded. Rotation has completed. Shutting down service.")

	// shut down the service
	cancel()
	// close the service without waiting for the connections to drain
	require.NoError(t, svc.Close())

	select {
	case err := <-runErrCh:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatalf("failed to shut down the server")
	}
}

// rotationConfig sets up default config used for CA rotation tests
func (s *integrationTestSuite) rotationConfig(disableWebService bool) *service.Config {
	tconf := s.defaultServiceConfig()
	tconf.SSH.Enabled = true
	tconf.Proxy.DisableWebService = disableWebService
	tconf.Proxy.DisableWebInterface = true
	tconf.Proxy.DisableDatabaseProxy = true
	tconf.Proxy.DisableALPNSNIListener = true
	tconf.PollingPeriod = time.Second
	tconf.ClientTimeout = time.Second
	tconf.ShutdownTimeout = 2 * tconf.ClientTimeout
	tconf.MaxRetryPeriod = time.Second
	return tconf
}

// waitForProcessEvent waits for process event to occur or timeout
func waitForProcessEvent(svc *service.TeleportProcess, event string, timeout time.Duration) error {
	if _, err := svc.WaitForEventTimeout(timeout, event); err != nil {
		return trace.BadParameter("timeout waiting for service to broadcast event %v", event)
	}
	return nil
}

// waitForProcessStart is waiting for the process to start
func waitForProcessStart(serviceC chan *service.TeleportProcess) (*service.TeleportProcess, error) {
	var svc *service.TeleportProcess
	select {
	case svc = <-serviceC:
	case <-time.After(1 * time.Minute):
		dumpGoroutineProfile()
		return nil, trace.BadParameter("timeout waiting for service to start")
	}
	return svc, nil
}

// waitForReload waits for multiple events to happen:
//
// 1. new service to be created and started
// 2. old service, if present to shut down
//
// this helper function allows to serialize tests for reloads.
func waitForReload(serviceC chan *service.TeleportProcess, old *service.TeleportProcess) (*service.TeleportProcess, error) {
	var svc *service.TeleportProcess
	select {
	case svc = <-serviceC:
	case <-time.After(1 * time.Minute):
		dumpGoroutineProfile()
		return nil, trace.BadParameter("timeout waiting for service to start")
	}

	if _, err := svc.WaitForEventTimeout(20*time.Second, service.TeleportReadyEvent); err != nil {
		dumpGoroutineProfile()
		return nil, trace.BadParameter("timeout waiting for service to broadcast ready status")
	}

	// if old service is present, wait for it to complete shut down procedure
	if old != nil {
		ctx, cancel := context.WithCancel(context.TODO())
		go func() {
			defer cancel()
			old.Supervisor.Wait()
		}()
		select {
		case <-ctx.Done():
		case <-time.After(1 * time.Minute):
			dumpGoroutineProfile()
			return nil, trace.BadParameter("timeout waiting for old service to stop")
		}
	}
	return svc, nil
}

// runAndMatch runs command and makes sure it matches the pattern
func runAndMatch(tc *client.TeleportClient, attempts int, command []string, pattern string) error {
	output := &bytes.Buffer{}
	tc.Stdout = output
	var err error
	for i := 0; i < attempts; i++ {
		err = tc.SSH(context.TODO(), command, false)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		out := output.String()
		out = replaceNewlines(out)
		matched, _ := regexp.MatchString(pattern, out)
		if matched {
			return nil
		}
		err = trace.CompareFailed("output %q did not match pattern %q", out, pattern)
		time.Sleep(500 * time.Millisecond)
	}
	return err
}

// TestWindowChange checks if custom Teleport window change requests are sent
// when the server side PTY changes its size.
func testWindowChange(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()
	ctx := context.Background()

	teleport := suite.newTeleport(t, nil, true)
	defer teleport.StopAll()

	site := teleport.GetSiteAPI(helpers.Site)
	require.NotNil(t, site)

	personA := NewTerminal(250)
	personB := NewTerminal(250)

	// openSession will open a new session on a server.
	openSession := func() {
		cl, err := teleport.NewClient(helpers.ClientConfig{
			Login:   suite.Me.Username,
			Cluster: helpers.Site,
			Host:    Host,
			Port:    helpers.Port(t, teleport.SSH),
		})
		require.NoError(t, err)

		cl.Stdout = personA
		cl.Stdin = personA

		err = cl.SSH(ctx, []string{}, false)
		if !isSSHError(err) {
			require.NoError(t, err)
		}
	}

	// joinSession will join the existing session on a server.
	joinSession := func() {
		// Find the existing session in the backend.
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sessions, err := waitForSessionToBeEstablished(timeoutCtx, defaults.Namespace, site)
		require.NoError(t, err)
		sessionID := sessions[0].GetSessionID()

		cl, err := teleport.NewClient(helpers.ClientConfig{
			Login:   suite.Me.Username,
			Cluster: helpers.Site,
			Host:    Host,
			Port:    helpers.Port(t, teleport.SSH),
		})
		require.NoError(t, err)

		cl.Stdout = personB
		cl.Stdin = personB

		// Change the size of the window immediately after it is created.
		cl.OnShellCreated = func(s *tracessh.Session, c *tracessh.Client, terminal io.ReadWriteCloser) (exit bool, err error) {
			err = s.WindowChange(ctx, 48, 160)
			if err != nil {
				return true, trace.Wrap(err)
			}
			return false, nil
		}

		for i := 0; i < 10; i++ {
			err = cl.Join(ctx, types.SessionPeerMode, defaults.Namespace, session.ID(sessionID), personB)
			if err == nil || isSSHError(err) {
				err = nil
				break
			}
		}

		require.NoError(t, err)
	}

	// waitForOutput checks that the output of the passed in terminal contains
	// one of the strings in `outputs` until some timeout has occurred.
	waitForOutput := func(t *Terminal, outputs ...string) error {
		tickerCh := time.Tick(500 * time.Millisecond)
		timeoutCh := time.After(30 * time.Second)
		for {
			select {
			case <-tickerCh:
				out := t.Output(5000)
				for _, s := range outputs {
					if strings.Contains(out, s) {
						return nil
					}
				}
			case <-timeoutCh:
				dumpGoroutineProfile()
				return trace.BadParameter("timed out waiting for output, last output: %q doesn't contain any of the expected substrings: %q", t.Output(5000), outputs)
			}
		}
	}

	// Open session, the initial size will be 80x24.
	go openSession()

	// Use the "printf" command to print the terminal size on the screen and
	// make sure it is 80x25.
	personA.Type("\atput cols; tput lines\n\r\a")
	err := waitForOutput(personA, "80\r\n25", "80\n\r25", "80\n25")
	require.NoError(t, err)

	// As soon as person B joins the session, the terminal is resized to 160x48.
	// Have another user join the session. As soon as the second shell is
	// created, the window is resized to 160x48 (see joinSession implementation).
	go joinSession()

	// Use the "printf" command to print the window size again and make sure it's
	// 160x48.
	personA.Type("\atput cols; tput lines\n\r\a")
	err = waitForOutput(personA, "160\r\n48", "160\n\r48", "160\n48")
	require.NoError(t, err)

	// Close the session.
	personA.Type("\aexit\r\n\a")
}

// testList checks that the list of servers returned is identity aware.
func testList(t *testing.T, suite *integrationTestSuite) {
	ctx := context.Background()

	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// Create and start a Teleport cluster with auth, proxy, and node.
	makeConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
		recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
			Mode: types.RecordOff,
		})
		require.NoError(t, err)

		tconf := suite.defaultServiceConfig()
		tconf.Hostname = "server-01"
		tconf.Auth.Enabled = true
		tconf.Auth.SessionRecordingConfig = recConfig
		tconf.Proxy.Enabled = true
		tconf.Proxy.DisableWebService = true
		tconf.Proxy.DisableWebInterface = true
		tconf.SSH.Enabled = true
		tconf.SSH.Labels = map[string]string{
			"role": "worker",
		}

		return t, nil, nil, tconf
	}
	teleport := suite.NewTeleportWithConfig(makeConfig())
	defer teleport.StopAll()

	// Create and start a Teleport node.
	nodeSSHPort := newPortValue()
	nodeConfig := func() *service.Config {
		tconf := suite.defaultServiceConfig()
		tconf.Hostname = "server-02"
		tconf.SSH.Enabled = true
		tconf.SSH.Addr.Addr = net.JoinHostPort(teleport.Hostname, fmt.Sprintf("%v", nodeSSHPort))
		tconf.SSH.Labels = map[string]string{
			"role": "database",
		}

		return tconf
	}
	_, err := teleport.StartNode(nodeConfig())
	require.NoError(t, err)

	// Get an auth client to the cluster.
	clt := teleport.GetSiteAPI(helpers.Site)
	require.NotNil(t, clt)

	// Wait 10 seconds for both nodes to show up to make sure they both have
	// registered themselves.
	waitForNodes := func(clt auth.ClientI, count int) error {
		tickCh := time.Tick(500 * time.Millisecond)
		stopCh := time.After(10 * time.Second)
		for {
			select {
			case <-tickCh:
				nodesInCluster, err := clt.GetNodes(ctx, defaults.Namespace)
				if err != nil && !trace.IsNotFound(err) {
					return trace.Wrap(err)
				}
				if got, want := len(nodesInCluster), count; got == want {
					return nil
				}
			case <-stopCh:
				return trace.BadParameter("waited 10s, did find %v nodes", count)
			}
		}
	}
	err = waitForNodes(clt, 2)
	require.NoError(t, err)

	tests := []struct {
		inRoleName string
		inLabels   types.Labels
		inLogin    string
		outNodes   []string
	}{
		// 0 - Role has label "role:worker", only server-01 is returned.
		{
			inRoleName: "worker-only",
			inLogin:    "foo",
			inLabels:   types.Labels{"role": []string{"worker"}},
			outNodes:   []string{"server-01"},
		},
		// 1 - Role has label "role:database", only server-02 is returned.
		{
			inRoleName: "database-only",
			inLogin:    "bar",
			inLabels:   types.Labels{"role": []string{"database"}},
			outNodes:   []string{"server-02"},
		},
		// 2 - Role has wildcard label, all nodes are returned server-01 and server-2.
		{
			inRoleName: "worker-and-database",
			inLogin:    "baz",
			inLabels:   types.Labels{types.Wildcard: []string{types.Wildcard}},
			outNodes:   []string{"server-01", "server-02"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.inRoleName, func(t *testing.T) {
			// Create role with logins and labels for this test.
			role, err := types.NewRoleV3(tt.inRoleName, types.RoleSpecV5{
				Allow: types.RoleConditions{
					Logins:     []string{tt.inLogin},
					NodeLabels: tt.inLabels,
				},
			})
			require.NoError(t, err)

			// Create user, role, and generate credentials.
			err = helpers.SetupUser(teleport.Process, tt.inLogin, []types.Role{role})
			require.NoError(t, err)
			initialCreds, err := helpers.GenerateUserCreds(helpers.UserCredsRequest{Process: teleport.Process, Username: tt.inLogin})
			require.NoError(t, err)

			// Create a Teleport client.
			cfg := helpers.ClientConfig{
				Login:   tt.inLogin,
				Cluster: helpers.Site,
				Port:    helpers.Port(t, teleport.SSH),
			}
			userClt, err := teleport.NewClientWithCreds(cfg, *initialCreds)
			require.NoError(t, err)

			// Get list of nodes and check that the returned nodes match the
			// expected nodes.
			nodes, err := userClt.ListNodesWithFilters(context.Background())
			require.NoError(t, err)
			for _, node := range nodes {
				ok := slices.Contains(tt.outNodes, node.GetHostname())
				if !ok {
					t.Fatalf("Got nodes: %v, want: %v.", nodes, tt.outNodes)
				}
			}
		})
	}
}

// TestCmdLabels verifies the behavior of running commands via labels
// with a mixture of regular and reversetunnel nodes.
func testCmdLabels(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// InsecureDevMode needed for IoT node handshake
	lib.SetInsecureDevMode(true)
	defer lib.SetInsecureDevMode(false)

	// Create and start a Teleport cluster with auth, proxy, and node.
	makeConfig := func() *service.Config {
		recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
			Mode: types.RecordOff,
		})
		require.NoError(t, err)

		tconf := suite.defaultServiceConfig()
		tconf.Hostname = "server-01"
		tconf.Auth.Enabled = true
		tconf.Auth.SessionRecordingConfig = recConfig
		tconf.Proxy.Enabled = true
		tconf.Proxy.DisableWebService = false
		tconf.Proxy.DisableWebInterface = true
		tconf.SSH.Enabled = true
		tconf.SSH.Labels = map[string]string{
			"role": "worker",
			"spam": "eggs",
		}

		return tconf
	}
	teleport := suite.NewTeleportWithConfig(t, nil, nil, makeConfig())
	defer teleport.StopAll()

	// Create and start a reversetunnel node.
	nodeConfig := func() *service.Config {
		tconf := suite.defaultServiceConfig()
		tconf.Hostname = "server-02"
		tconf.SSH.Enabled = true
		tconf.SSH.Labels = map[string]string{
			"role": "database",
			"spam": "eggs",
		}

		return tconf
	}
	_, err := teleport.StartReverseTunnelNode(nodeConfig())
	require.NoError(t, err)

	// test label patterns that match both nodes, and each
	// node individually.
	tts := []struct {
		desc    string
		command []string
		labels  map[string]string
		expect  string
	}{
		{
			desc:    "Both",
			command: []string{"echo", "two"},
			labels:  map[string]string{"spam": "eggs"},
			expect:  "two\ntwo\n",
		},
		{
			desc:    "Worker only",
			command: []string{"echo", "worker"},
			labels:  map[string]string{"role": "worker"},
			expect:  "worker\n",
		},
		{
			desc:    "Database only",
			command: []string{"echo", "database"},
			labels:  map[string]string{"role": "database"},
			expect:  "database\n",
		},
	}

	for _, tt := range tts {
		t.Run(tt.desc, func(t *testing.T) {
			cfg := helpers.ClientConfig{
				Login:   suite.Me.Username,
				Cluster: helpers.Site,
				Labels:  tt.labels,
			}

			output, err := runCommand(t, teleport, tt.command, cfg, 1)
			require.NoError(t, err)
			require.Equal(t, tt.expect, output)
		})
	}
}

// TestDataTransfer makes sure that a "session.data" event is emitted at the
// end of a session that matches the amount of data that was transferred.
func testDataTransfer(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	KB := 1024
	MB := 1048576

	// Create a Teleport cluster.
	main := suite.newTeleport(t, nil, true)
	defer main.StopAll()

	// Create a client to the above Teleport cluster.
	clientConfig := helpers.ClientConfig{
		Login:   suite.Me.Username,
		Cluster: helpers.Site,
		Host:    Host,
		Port:    helpers.Port(t, main.SSH),
	}

	// Write 1 MB to stdout.
	command := []string{"dd", "if=/dev/zero", "bs=1024", "count=1024"}
	output, err := runCommand(t, main, command, clientConfig, 1)
	require.NoError(t, err)

	// Make sure exactly 1 MB was written to output.
	require.Len(t, output, MB)

	// Make sure the session.data event was emitted to the audit log.
	eventFields, err := findEventInLog(main, events.SessionDataEvent)
	require.NoError(t, err)

	// Make sure the audit event shows that 1 MB was written to the output.
	require.Greater(t, eventFields.GetInt(events.DataReceived), MB)
	require.Greater(t, eventFields.GetInt(events.DataTransmitted), KB)
}

func testBPFInteractive(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// Check if BPF tests can be run on this host.
	err := canTestBPF()
	if err != nil {
		t.Skipf("Tests for BPF functionality can not be run: %v.", err)
		return
	}

	lsPath, err := exec.LookPath("ls")
	require.NoError(t, err)

	tests := []struct {
		desc               string
		inSessionRecording string
		inBPFEnabled       bool
		outFound           bool
	}{
		// For session recorded at the node, enhanced events should be found.
		{
			desc:               "Enabled and Recorded At Node",
			inSessionRecording: types.RecordAtNode,
			inBPFEnabled:       true,
			outFound:           true,
		},
		// For session recorded at the node, but BPF is turned off, no events
		// should be found.
		{
			desc:               "Disabled and Recorded At Node",
			inSessionRecording: types.RecordAtNode,
			inBPFEnabled:       false,
			outFound:           false,
		},
		// For session recorded at the proxy, enhanced events should not be found.
		// BPF turned off simulates an OpenSSH node.
		{
			desc:               "Disabled and Recorded At Proxy",
			inSessionRecording: types.RecordAtProxy,
			inBPFEnabled:       false,
			outFound:           false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			// Create temporary directory where cgroup2 hierarchy will be mounted.
			dir := t.TempDir()

			// Create and start a Teleport cluster.
			makeConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
				recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
					Mode: tt.inSessionRecording,
				})
				require.NoError(t, err)

				// Create default config.
				tconf := suite.defaultServiceConfig()

				// Configure Auth.
				tconf.Auth.Preference.SetSecondFactor("off")
				tconf.Auth.Enabled = true
				tconf.Auth.SessionRecordingConfig = recConfig

				// Configure Proxy.
				tconf.Proxy.Enabled = true
				tconf.Proxy.DisableWebService = false
				tconf.Proxy.DisableWebInterface = true

				// Configure Node. If session are being recorded at the proxy, don't enable
				// BPF to simulate an OpenSSH node.
				tconf.SSH.Enabled = true
				if tt.inBPFEnabled {
					tconf.SSH.BPF.Enabled = true
					tconf.SSH.BPF.CgroupPath = dir
				}
				return t, nil, nil, tconf
			}
			main := suite.NewTeleportWithConfig(makeConfig())
			defer main.StopAll()

			// Create a client terminal and context to signal when the client is done
			// with the terminal.
			term := NewTerminal(250)
			doneContext, doneCancel := context.WithCancel(context.Background())

			func() {
				client, err := main.NewClient(helpers.ClientConfig{
					Login:   suite.Me.Username,
					Cluster: helpers.Site,
					Host:    Host,
					Port:    helpers.Port(t, main.SSH),
				})
				require.NoError(t, err)

				// Connect terminal to std{in,out} of client.
				client.Stdout = term
				client.Stdin = term

				// "Type" a command into the terminal.
				term.Type(fmt.Sprintf("\a%v\n\r\aexit\n\r\a", lsPath))
				err = client.SSH(context.TODO(), []string{}, false)
				require.NoError(t, err)

				// Signal that the client has finished the interactive session.
				doneCancel()
			}()

			// Wait 10 seconds for the client to finish up the interactive session.
			select {
			case <-time.After(10 * time.Second):
				t.Fatalf("Timed out waiting for client to finish interactive session.")
			case <-doneContext.Done():
			}

			// Enhanced events should show up for session recorded at the node but not
			// at the proxy.
			if tt.outFound {
				_, err = findCommandEventInLog(main, events.SessionCommandEvent, lsPath)
				require.NoError(t, err)
			} else {
				_, err = findCommandEventInLog(main, events.SessionCommandEvent, lsPath)
				require.Error(t, err)
			}
		})
	}
}

func testBPFExec(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// Check if BPF tests can be run on this host.
	err := canTestBPF()
	if err != nil {
		t.Skipf("Tests for BPF functionality can not be run: %v.", err)
		return
	}

	lsPath, err := exec.LookPath("ls")
	require.NoError(t, err)

	tests := []struct {
		desc               string
		inSessionRecording string
		inBPFEnabled       bool
		outFound           bool
	}{
		// For session recorded at the node, enhanced events should be found.
		{
			desc:               "Enabled and recorded at node",
			inSessionRecording: types.RecordAtNode,
			inBPFEnabled:       true,
			outFound:           true,
		},
		// For session recorded at the node, but BPF is turned off, no events
		// should be found.
		{
			desc:               "Disabled and recorded at node",
			inSessionRecording: types.RecordAtNode,
			inBPFEnabled:       false,
			outFound:           false,
		},
		// For session recorded at the proxy, enhanced events should not be found.
		// BPF turned off simulates an OpenSSH node.
		{
			desc:               "Disabled and recorded at proxy",
			inSessionRecording: types.RecordAtProxy,
			inBPFEnabled:       false,
			outFound:           false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			// Create temporary directory where cgroup2 hierarchy will be mounted.
			dir := t.TempDir()

			// Create and start a Teleport cluster.
			makeConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
				recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
					Mode: tt.inSessionRecording,
				})
				require.NoError(t, err)

				// Create default config.
				tconf := suite.defaultServiceConfig()

				// Configure Auth.
				tconf.Auth.Preference.SetSecondFactor("off")
				tconf.Auth.Enabled = true
				tconf.Auth.SessionRecordingConfig = recConfig

				// Configure Proxy.
				tconf.Proxy.Enabled = true
				tconf.Proxy.DisableWebService = false
				tconf.Proxy.DisableWebInterface = true

				// Configure Node. If session are being recorded at the proxy, don't enable
				// BPF to simulate an OpenSSH node.
				tconf.SSH.Enabled = true
				if tt.inBPFEnabled {
					tconf.SSH.BPF.Enabled = true
					tconf.SSH.BPF.CgroupPath = dir
				}
				return t, nil, nil, tconf
			}
			main := suite.NewTeleportWithConfig(makeConfig())
			defer main.StopAll()

			// Create a client to the above Teleport cluster.
			clientConfig := helpers.ClientConfig{
				Login:   suite.Me.Username,
				Cluster: helpers.Site,
				Host:    Host,
				Port:    helpers.Port(t, main.SSH),
			}

			// Run exec command.
			_, err = runCommand(t, main, []string{lsPath}, clientConfig, 1)
			require.NoError(t, err)

			// Enhanced events should show up for session recorded at the node but not
			// at the proxy.
			if tt.outFound {
				_, err = findCommandEventInLog(main, events.SessionCommandEvent, lsPath)
				require.NoError(t, err)
			} else {
				_, err = findCommandEventInLog(main, events.SessionCommandEvent, lsPath)
				require.Error(t, err)
			}
		})
	}
}

func testSSHExitCode(t *testing.T, suite *integrationTestSuite) {
	lsPath, err := exec.LookPath("ls")
	require.NoError(t, err)

	tests := []struct {
		desc           string
		command        []string
		input          string
		interactive    bool
		errorAssertion require.ErrorAssertionFunc
		statusCode     int
	}{
		// A successful noninteractive session should have a zero status code
		{
			desc:           "Run Command and Exit Successfully",
			command:        []string{lsPath},
			interactive:    false,
			errorAssertion: require.NoError,
		},
		// A failed noninteractive session should have a non-zero status code
		{
			desc:           "Run Command and Fail With Code 2",
			command:        []string{"exit 2"},
			interactive:    false,
			errorAssertion: require.Error,
			statusCode:     2,
		},
		// A failed interactive session should have a non-zero status code
		{
			desc:           "Run Command Interactively and Fail With Code 2",
			command:        []string{"exit 2"},
			interactive:    true,
			errorAssertion: require.Error,
			statusCode:     2,
		},
		// A failed interactive session should have a non-zero status code
		{
			desc:           "Interactively Fail With Code 3",
			input:          "exit 3\n\r",
			interactive:    true,
			errorAssertion: require.Error,
			statusCode:     3,
		},
		// A failed interactive session should have a non-zero status code
		{
			desc:           "Interactively Fail With Code 3",
			input:          fmt.Sprintf("%v\n\rexit 3\n\r", lsPath),
			interactive:    true,
			errorAssertion: require.Error,
			statusCode:     3,
		},
		// A successful interactive session should have a zero status code
		{
			desc:           "Interactively Run Command and Exit Successfully",
			input:          fmt.Sprintf("%v\n\rexit\n\r", lsPath),
			interactive:    true,
			errorAssertion: require.NoError,
		},
		// A successful interactive session should have a zero status code
		{
			desc:           "Interactively Exit",
			input:          "exit\n\r",
			interactive:    true,
			errorAssertion: require.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			// Create and start a Teleport cluster.
			makeConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
				// Create default config.
				tconf := suite.defaultServiceConfig()

				// Configure Auth.
				tconf.Auth.Preference.SetSecondFactor("off")
				tconf.Auth.Enabled = true
				tconf.Auth.NoAudit = true

				// Configure Proxy.
				tconf.Proxy.Enabled = true
				tconf.Proxy.DisableWebService = false
				tconf.Proxy.DisableWebInterface = true

				// Configure Node.
				tconf.SSH.Enabled = true
				return t, nil, nil, tconf
			}
			main := suite.NewTeleportWithConfig(makeConfig())
			t.Cleanup(func() { main.StopAll() })

			// context to signal when the client is done with the terminal.
			doneContext, doneCancel := context.WithTimeout(context.Background(), time.Second*10)
			defer doneCancel()

			cli, err := main.NewClient(helpers.ClientConfig{
				Login:       suite.Me.Username,
				Cluster:     helpers.Site,
				Host:        Host,
				Port:        helpers.Port(t, main.SSH),
				Interactive: tt.interactive,
			})
			require.NoError(t, err)

			if tt.interactive {
				// Create a new terminal and connect it to std{in,out} of client.
				term := NewTerminal(250)
				cli.Stdout = term
				cli.Stdin = term
				term.Type(tt.input)
			}

			// run the ssh command
			err = cli.SSH(doneContext, tt.command, false)
			tt.errorAssertion(t, err)

			// check that the exit code of the session matches the expected one
			if err != nil {
				var exitError *ssh.ExitError
				require.ErrorAs(t, trace.Unwrap(err), &exitError)
				require.Equal(t, tt.statusCode, exitError.ExitStatus())
			}
		})
	}
}

// testBPFSessionDifferentiation verifies that the bpf package can
// differentiate events from two different sessions. This test in turn also
// verifies the cgroup package.
func testBPFSessionDifferentiation(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// Check if BPF tests can be run on this host.
	err := canTestBPF()
	if err != nil {
		t.Skipf("Tests for BPF functionality can not be run: %v.", err)
		return
	}

	lsPath, err := exec.LookPath("ls")
	require.NoError(t, err)

	// Create temporary directory where cgroup2 hierarchy will be mounted.
	dir := t.TempDir()

	// Create and start a Teleport cluster.
	makeConfig := func() (*testing.T, []string, []*helpers.InstanceSecrets, *service.Config) {
		recConfig, err := types.NewSessionRecordingConfigFromConfigFile(types.SessionRecordingConfigSpecV2{
			Mode: types.RecordAtNode,
		})
		require.NoError(t, err)

		// Create default config.
		tconf := suite.defaultServiceConfig()

		// Configure Auth.
		tconf.Auth.Preference.SetSecondFactor("off")
		tconf.Auth.Enabled = true
		tconf.Auth.SessionRecordingConfig = recConfig

		// Configure Proxy.
		tconf.Proxy.Enabled = true
		tconf.Proxy.DisableWebService = false
		tconf.Proxy.DisableWebInterface = true

		// Configure Node. If session are being recorded at the proxy, don't enable
		// BPF to simulate an OpenSSH node.
		tconf.SSH.Enabled = true
		tconf.SSH.BPF.Enabled = true
		tconf.SSH.BPF.CgroupPath = dir
		return t, nil, nil, tconf
	}
	main := suite.NewTeleportWithConfig(makeConfig())
	defer main.StopAll()

	// Create two client terminals and channel to signal when the clients are
	// done with the terminals.
	termA := NewTerminal(250)
	termB := NewTerminal(250)
	doneCh := make(chan bool, 2)

	// Open a terminal and type "ls" into both and exit.
	writeTerm := func(term *Terminal) {
		client, err := main.NewClient(helpers.ClientConfig{
			Login:   suite.Me.Username,
			Cluster: helpers.Site,
			Host:    Host,
			Port:    helpers.Port(t, main.SSH),
		})
		if err != nil {
			t.Errorf("Failed to create client: %v.", err)
		}

		// Connect terminal to std{in,out} of client.
		client.Stdout = term
		client.Stdin = term

		// "Type" a command into the terminal.
		term.Type(fmt.Sprintf("\a%v\n\r\aexit\n\r\a", lsPath))
		err = client.SSH(context.Background(), []string{}, false)
		if err != nil {
			t.Errorf("Failed to start SSH session: %v.", err)
		}

		// Signal that the client has finished the interactive session.
		doneCh <- true
	}

	// It's possible to run this test sequentially but it should
	// be run in parallel to amortize the time since the two tasks can be run in parallel.
	//
	// This is also important because it ensures the tests faults if some part of the SSH code
	// hangs unexpectedly instead of timing out silently.
	go writeTerm(termA)
	go writeTerm(termB)

	// Wait 10 seconds for both events to arrive, otherwise timeout.
	timeout := time.After(10 * time.Second)
	gotEvents := 0
	for {
		select {
		case <-doneCh:
			gotEvents++
		case <-timeout:
			require.FailNow(t, "Timed out waiting for client to finish interactive session.")
		}
		if gotEvents == 2 {
			break
		}
	}

	// Try to find two command events from different sessions. Timeout after
	// 10 seconds.
	for i := 0; i < 10; i++ {
		sessionIDs := map[string]bool{}

		eventFields, err := eventsInLog(main.Config.DataDir+"/log/events.log", events.SessionCommandEvent)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		for _, fields := range eventFields {
			if fields.GetString(events.EventType) == events.SessionCommandEvent &&
				fields.GetString(events.Path) == lsPath {
				sessionIDs[fields.GetString(events.SessionEventID)] = true
			}
		}

		// If two command events for "ls" from different sessions, return right
		// away, test was successful.
		if len(sessionIDs) == 2 {
			return
		}
		time.Sleep(1 * time.Second)
	}
	require.Fail(t, "Failed to find command events from two different sessions.")
}

// testExecEvents tests if exec events were emitted with and without PTY allocated
func testExecEvents(t *testing.T, suite *integrationTestSuite) {
	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	// Creates new teleport cluster
	main := suite.newTeleport(t, nil, true)
	defer main.StopAll()

	// Max event size for file log (bufio.MaxScanTokenSize) should be 64k, make
	// a command much larger than that.
	lotsOfBytes := bytes.Repeat([]byte{'a'}, 100*1024)

	execTests := []struct {
		name          string
		isInteractive bool
		command       string
	}{
		{
			name:          "PTY allocated",
			isInteractive: true,
			command:       "echo 1",
		},
		{
			name:          "PTY not allocated",
			isInteractive: false,
			command:       "echo 2",
		},
		{
			name:          "long command interactive",
			isInteractive: true,
			command:       "true 1 " + string(lotsOfBytes),
		},
		{
			name:          "long command uninteractive",
			isInteractive: false,
			command:       "true 2 " + string(lotsOfBytes),
		},
	}

	for _, tt := range execTests {
		t.Run(tt.name, func(t *testing.T) {
			// Create client for each test in grid tests
			clientConfig := helpers.ClientConfig{
				Login:       suite.Me.Username,
				Cluster:     helpers.Site,
				Host:        Host,
				Port:        helpers.Port(t, main.SSH),
				Interactive: tt.isInteractive,
			}
			_, err := runCommand(t, main, []string{tt.command}, clientConfig, 1)
			require.NoError(t, err)

			expectedCommandPrefix := tt.command
			if len(expectedCommandPrefix) > 32 {
				expectedCommandPrefix = expectedCommandPrefix[:32]
			}

			// Make sure the session start event was emitted to the audit log
			// and includes (a prefix of) the command
			_, err = findMatchingEventInLog(main, events.SessionStartEvent, func(fields events.EventFields) bool {
				initialCommand := fields.GetStrings("initial_command")
				return events.SessionStartCode == fields.GetCode() && len(initialCommand) == 1 &&
					strings.HasPrefix(initialCommand[0], expectedCommandPrefix)
			})
			require.NoError(t, err)

			// Make sure the exec event was emitted to the audit log.
			_, err = findMatchingEventInLog(main, events.ExecEvent, func(fields events.EventFields) bool {
				return events.ExecCode == fields.GetCode() &&
					strings.HasPrefix(fields.GetString(events.ExecEventCommand), expectedCommandPrefix)
			})
			require.NoError(t, err)
		})
	}

	t.Run("long running", func(t *testing.T) {
		clientConfig := helpers.ClientConfig{
			Login:       suite.Me.Username,
			Cluster:     helpers.Site,
			Host:        Host,
			Port:        helpers.Port(t, main.SSH),
			Interactive: false,
		}
		ctx, cancel := context.WithCancel(context.Background())

		cmd := "sleep 10"

		errC := make(chan error)
		go func() {
			_, err := runCommandWithContext(ctx, t, main, []string{cmd}, clientConfig, 1)
			errC <- err
		}()

		// Make sure the session start event was emitted immediately to the audit log
		// before waiting for the command to complete, and includes the command
		startEvent, err := findMatchingEventInLog(main, events.SessionStartEvent, func(fields events.EventFields) bool {
			initialCommand := fields.GetStrings("initial_command")
			return len(initialCommand) == 1 && initialCommand[0] == cmd
		})
		require.NoError(t, err)

		sessionID := startEvent.GetString(events.SessionEventID)
		require.NotEmpty(t, sessionID)

		cancel()
		// This may or may not be an error, depending on whether we canceled it
		// before the command died of natural causes, no need to test the value
		// here but we'll wait for it in order to avoid leaking goroutines
		<-errC

		// Wait for the session end event to avoid writes to the tempdir after
		// the test completes (and make sure it's actually sent)
		require.Eventually(t, func() bool {
			_, err := findMatchingEventInLog(main, events.SessionEndEvent, func(fields events.EventFields) bool {
				return sessionID == fields.GetString(events.SessionEventID)
			})
			return err == nil
		}, 30*time.Second, 1*time.Second)
	})
}

func testSessionStartContainsAccessRequest(t *testing.T, suite *integrationTestSuite) {
	accessRequestsKey := "access_requests"
	requestedRoleName := "requested-role"
	userRoleName := "user-role"

	tr := utils.NewTracer(utils.ThisFunction()).Start()
	defer tr.Stop()

	lsPath, err := exec.LookPath("ls")
	require.NoError(t, err)

	// Creates new teleport cluster
	main := suite.newTeleport(t, nil, true)
	defer main.StopAll()

	ctx := context.Background()
	// Get auth server
	authServer := main.Process.GetAuthServer()

	// Create new request role
	requestedRole, err := types.NewRoleV3(requestedRoleName, types.RoleSpecV5{
		Options: types.RoleOptions{},
		Allow:   types.RoleConditions{},
	})
	require.NoError(t, err)

	err = authServer.UpsertRole(ctx, requestedRole)
	require.NoError(t, err)

	// Create user role with ability to request role
	userRole, err := types.NewRoleV3(userRoleName, types.RoleSpecV5{
		Options: types.RoleOptions{},
		Allow: types.RoleConditions{
			Logins: []string{
				suite.Me.Username,
			},
			Request: &types.AccessRequestConditions{
				Roles: []string{requestedRoleName},
			},
		},
	})
	require.NoError(t, err)

	err = authServer.UpsertRole(ctx, userRole)
	require.NoError(t, err)

	user, err := types.NewUser(suite.Me.Username)
	user.AddRole(userRole.GetName())
	require.NoError(t, err)

	watcher, err := authServer.NewWatcher(ctx, types.Watch{
		Kinds: []types.WatchKind{
			{Kind: types.KindUser},
			{Kind: types.KindAccessRequest},
		},
	})
	require.NoError(t, err)
	defer watcher.Close()

	select {
	case <-time.After(time.Second * 30):
		t.Fatalf("Timeout waiting for event.")
	case event := <-watcher.Events():
		if event.Type != types.OpInit {
			t.Fatalf("Unexpected event type.")
		}
		require.Equal(t, event.Type, types.OpInit)
	case <-watcher.Done():
		t.Fatal(watcher.Error())
	}

	// Update user
	err = authServer.UpsertUser(user)
	require.NoError(t, err)

	WaitForResource(t, watcher, user.GetKind(), user.GetName())

	req, err := services.NewAccessRequest(suite.Me.Username, requestedRole.GetMetadata().Name)
	require.NoError(t, err)

	accessRequestID := req.GetName()

	err = authServer.CreateAccessRequest(context.TODO(), req)
	require.NoError(t, err)

	err = authServer.SetAccessRequestState(context.TODO(), types.AccessRequestUpdate{
		RequestID: accessRequestID,
		State:     types.RequestState_APPROVED,
	})
	require.NoError(t, err)

	WaitForResource(t, watcher, req.GetKind(), req.GetName())

	clientConfig := helpers.ClientConfig{
		Login:       suite.Me.Username,
		Cluster:     helpers.Site,
		Host:        Host,
		Port:        helpers.Port(t, main.SSH),
		Interactive: false,
	}
	clientReissueParams := client.ReissueParams{
		AccessRequests: []string{accessRequestID},
	}
	err = runCommandWithCertReissue(t, main, []string{lsPath}, clientReissueParams, client.CertCacheDrop, clientConfig)
	require.NoError(t, err)

	// Get session start event
	sessionStart, err := findEventInLog(main, events.SessionStartEvent)
	require.NoError(t, err)
	require.Equal(t, sessionStart.GetCode(), events.SessionStartCode)
	require.Equal(t, sessionStart.HasField(accessRequestsKey), true)

	val, found := sessionStart[accessRequestsKey]
	require.Equal(t, found, true)

	result := strings.Contains(fmt.Sprintf("%v", val), accessRequestID)
	require.Equal(t, result, true)
}

func WaitForResource(t *testing.T, watcher types.Watcher, kind, name string) {
	timeout := time.After(time.Second * 15)
	for {
		select {
		case <-timeout:
			t.Fatalf("Timeout waiting for event.")
		case event := <-watcher.Events():
			if event.Type != types.OpPut {
				continue
			}
			if event.Resource.GetKind() == kind && event.Resource.GetMetadata().Name == name {
				return
			}
		case <-watcher.Done():
			t.Fatalf("Watcher error %s.", watcher.Error())
		}
	}
}

// findEventInLog polls the event log looking for an event of a particular type.
func findEventInLog(t *helpers.TeleInstance, eventName string) (events.EventFields, error) {
	for i := 0; i < 10; i++ {
		eventFields, err := eventsInLog(t.Config.DataDir+"/log/events.log", eventName)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		for _, fields := range eventFields {
			eventType, ok := fields[events.EventType]
			if !ok {
				return nil, trace.BadParameter("not found")
			}
			if eventType == eventName {
				return fields, nil
			}
		}

		time.Sleep(250 * time.Millisecond)
	}
	return nil, trace.NotFound("event not found")
}

// findCommandEventInLog polls the event log looking for an event of a particular type.
func findCommandEventInLog(t *helpers.TeleInstance, eventName string, programName string) (events.EventFields, error) {
	return findMatchingEventInLog(t, eventName, func(fields events.EventFields) bool {
		eventType := fields[events.EventType]
		eventPath := fields[events.Path]
		return eventType == eventName && eventPath == programName
	})
}

func findMatchingEventInLog(t *helpers.TeleInstance, eventName string, match func(events.EventFields) bool) (events.EventFields, error) {
	for i := 0; i < 10; i++ {
		eventFields, err := eventsInLog(t.Config.DataDir+"/log/events.log", eventName)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		for _, fields := range eventFields {
			if match(fields) {
				return fields, nil
			}
		}

		time.Sleep(1 * time.Second)
	}
	return nil, trace.NotFound("event not found")
}

// eventsInLog returns all events in a log file.
func eventsInLog(path string, eventName string) ([]events.EventFields, error) {
	var ret []events.EventFields

	file, err := os.Open(path)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var fields events.EventFields
		err = json.Unmarshal(scanner.Bytes(), &fields)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		ret = append(ret, fields)
	}

	if len(ret) == 0 {
		return nil, trace.NotFound("event not found")
	}
	return ret, nil
}

// runCommandWithCertReissue runs an SSH command and generates certificates for the user
func runCommandWithCertReissue(t *testing.T, instance *helpers.TeleInstance, cmd []string, reissueParams client.ReissueParams, cachePolicy client.CertCachePolicy, cfg helpers.ClientConfig) error {
	tc, err := instance.NewClient(cfg)
	if err != nil {
		return trace.Wrap(err)
	}

	err = tc.ReissueUserCerts(context.Background(), cachePolicy, reissueParams)
	if err != nil {
		return trace.Wrap(err)
	}

	out := &bytes.Buffer{}
	tc.Stdout = out

	err = tc.SSH(context.TODO(), cmd, false)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

func runCommandWithContext(ctx context.Context, t *testing.T, instance *helpers.TeleInstance, cmd []string, cfg helpers.ClientConfig, attempts int) (string, error) {
	tc, err := instance.NewClient(cfg)
	if err != nil {
		return "", trace.Wrap(err)
	}
	// since this helper is sometimes used for running commands on
	// multiple nodes concurrently, we use io.Pipe to protect our
	// output buffer from concurrent writes.
	read, write := io.Pipe()
	output := &bytes.Buffer{}
	doneC := make(chan struct{})
	go func() {
		io.Copy(output, read)
		close(doneC)
	}()
	tc.Stdout = write
	for i := 0; i < attempts; i++ {
		err = tc.SSH(ctx, cmd, false)
		if err == nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
	write.Close()
	if err != nil {
		return "", trace.Wrap(err)
	}
	<-doneC
	return output.String(), nil
}

// invoke makes it easier to defer multiple cancelFuncs held by the same variable
// without them stomping on one another.
func invoke(cancel context.CancelFunc) {
	cancel()
}

// runCommand is a shortcut for running SSH command, it creates a client
// connected to proxy of the passed in instance, runs the command, and returns
// the result. If multiple attempts are requested, a 250 millisecond delay is
// added between them before giving up.
func runCommand(t *testing.T, instance *helpers.TeleInstance, cmd []string, cfg helpers.ClientConfig, attempts int) (string, error) {
	return runCommandWithContext(context.TODO(), t, instance, cmd, cfg, attempts)
}

type InstanceConfigOption func(t *testing.T, config *helpers.InstanceConfig)

func (s *integrationTestSuite) newNamedTeleportInstance(t *testing.T, clusterName string, opts ...InstanceConfigOption) *helpers.TeleInstance {
	cfg := helpers.InstanceConfig{
		ClusterName: clusterName,
		HostID:      helpers.HostID,
		NodeName:    Host,
		Priv:        s.Priv,
		Pub:         s.Pub,
		Log:         utils.WrapLogger(s.Log.WithField("cluster", clusterName)),
	}

	for _, opt := range opts {
		opt(t, &cfg)
	}

	if cfg.Listeners == nil {
		cfg.Listeners = helpers.StandardListenerSetupOn(cfg.NodeName)(t, &cfg.Fds)
	}

	return helpers.NewInstance(t, cfg)
}

func WithNodeName(nodeName string) InstanceConfigOption {
	return func(_ *testing.T, config *helpers.InstanceConfig) {
		config.NodeName = nodeName
	}
}

func WithListeners(setupFn helpers.InstanceListenerSetupFunc) InstanceConfigOption {
	return func(t *testing.T, config *helpers.InstanceConfig) {
		config.Listeners = setupFn(t, &config.Fds)
	}
}

func (s *integrationTestSuite) defaultServiceConfig() *service.Config {
	cfg := service.MakeDefaultConfig()
	cfg.Console = nil
	cfg.Log = s.Log
	cfg.CircuitBreakerConfig = breaker.NoopBreakerConfig()
	return cfg
}

// waitFor helper waits on a channel for up to the given timeout
func waitFor(c chan interface{}, timeout time.Duration) error {
	tick := time.Tick(timeout)
	select {
	case <-c:
		return nil
	case <-tick:
		return trace.LimitExceeded("timeout waiting for event")
	}
}

// waitForError helper waits on an error channel for up to the given timeout
func waitForError(c chan error, timeout time.Duration) error {
	tick := time.Tick(timeout)
	select {
	case err := <-c:
		return err
	case <-tick:
		return trace.LimitExceeded("timeout waiting for event")
	}
}

// hasPAMPolicy checks if the three policy files needed for tests exists. If
// they do it returns true, otherwise returns false.
func hasPAMPolicy() bool {
	pamPolicyFiles := []string{
		"/etc/pam.d/teleport-acct-failure",
		"/etc/pam.d/teleport-session-failure",
		"/etc/pam.d/teleport-success",
		"/etc/pam.d/teleport-custom-env",
	}

	for _, fileName := range pamPolicyFiles {
		_, err := os.Stat(fileName)
		if os.IsNotExist(err) {
			return false
		}
	}

	return true
}

// isRoot returns a boolean if the test is being run as root or not.
func isRoot() bool {
	return os.Geteuid() == 0
}

// canTestBPF runs checks to determine whether BPF tests will run or not.
// Tests for this package must be run as root.
func canTestBPF() error {
	if !isRoot() {
		return trace.BadParameter("not root")
	}

	err := bpf.IsHostCompatible()
	if err != nil {
		return trace.Wrap(err)
	}

	return nil
}

func dumpGoroutineProfile() {
	pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
}

// TestWebProxyInsecure makes sure that proxy endpoint works when TLS is disabled.
func TestWebProxyInsecure(t *testing.T) {
	privateKey, publicKey, err := testauthority.New().GenerateKeyPair()
	require.NoError(t, err)

	rc := helpers.NewInstance(t, helpers.InstanceConfig{
		ClusterName: "example.com",
		HostID:      uuid.New().String(),
		NodeName:    Host,
		Priv:        privateKey,
		Pub:         publicKey,
		Log:         utils.NewLoggerForTests(),
	})

	rcConf := service.MakeDefaultConfig()
	rcConf.DataDir = t.TempDir()
	rcConf.Auth.Enabled = true
	rcConf.Auth.Preference.SetSecondFactor("off")
	rcConf.Proxy.Enabled = true
	rcConf.Proxy.DisableWebInterface = true
	// DisableTLS flag should turn off TLS termination and multiplexing.
	rcConf.Proxy.DisableTLS = true
	rcConf.CircuitBreakerConfig = breaker.NoopBreakerConfig()

	err = rc.CreateEx(t, nil, rcConf)
	require.NoError(t, err)

	err = rc.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		rc.StopAll()
	})

	// Web proxy endpoint should just respond with 200 when called over http://,
	// content doesn't matter.
	resp, err := http.Get(fmt.Sprintf("http://%v/webapi/ping", rc.Web))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

// TestTraitsPropagation makes sure that user traits are applied properly to
// roles in root and leaf clusters.
func TestTraitsPropagation(t *testing.T) {
	log := utils.NewLoggerForTests()

	privateKey, publicKey, err := testauthority.New().GenerateKeyPair()
	require.NoError(t, err)

	// Create root cluster.
	rc := helpers.NewInstance(t, helpers.InstanceConfig{
		ClusterName: "root.example.com",
		HostID:      uuid.New().String(),
		NodeName:    Host,
		Priv:        privateKey,
		Pub:         publicKey,
		Log:         log,
	})

	// Create leaf cluster.
	lc := helpers.NewInstance(t, helpers.InstanceConfig{
		ClusterName: "leaf.example.com",
		HostID:      uuid.New().String(),
		NodeName:    Host,
		Priv:        privateKey,
		Pub:         publicKey,
		Log:         log,
	})

	// Make root cluster config.
	rcConf := service.MakeDefaultConfig()
	rcConf.DataDir = t.TempDir()
	rcConf.Auth.Enabled = true
	rcConf.Auth.Preference.SetSecondFactor("off")
	rcConf.Proxy.Enabled = true
	rcConf.Proxy.DisableWebService = true
	rcConf.Proxy.DisableWebInterface = true
	rcConf.SSH.Enabled = true
	rcConf.SSH.Addr.Addr = rc.SSH
	rcConf.SSH.Labels = map[string]string{"env": "integration"}
	rcConf.CircuitBreakerConfig = breaker.NoopBreakerConfig()

	// Make leaf cluster config.
	lcConf := service.MakeDefaultConfig()
	lcConf.DataDir = t.TempDir()
	lcConf.Auth.Enabled = true
	lcConf.Auth.Preference.SetSecondFactor("off")
	lcConf.Proxy.Enabled = true
	lcConf.Proxy.DisableWebInterface = true
	lcConf.SSH.Enabled = true
	lcConf.SSH.Addr.Addr = lc.SSH
	lcConf.SSH.Labels = map[string]string{"env": "integration"}
	lcConf.CircuitBreakerConfig = breaker.NoopBreakerConfig()

	// Create identical user/role in both clusters.
	me, err := user.Current()
	require.NoError(t, err)

	role := services.NewImplicitRole()
	role.SetName("test")
	role.SetLogins(types.Allow, []string{me.Username})
	// Users created by CreateEx have "testing: integration" trait.
	role.SetNodeLabels(types.Allow, map[string]apiutils.Strings{"env": []string{"{{external.testing}}"}})

	rc.AddUserWithRole(me.Username, role)
	lc.AddUserWithRole(me.Username, role)

	// Establish trust b/w root and leaf.
	err = rc.CreateEx(t, lc.Secrets.AsSlice(), rcConf)
	require.NoError(t, err)
	err = lc.CreateEx(t, rc.Secrets.AsSlice(), lcConf)
	require.NoError(t, err)

	// Start both clusters.
	require.NoError(t, rc.Start())
	t.Cleanup(func() {
		rc.StopAll()
	})
	require.NoError(t, lc.Start())
	t.Cleanup(func() {
		lc.StopAll()
	})

	// Update root's certificate authority on leaf to configure role mapping.
	ca, err := lc.Process.GetAuthServer().GetCertAuthority(context.Background(), types.CertAuthID{
		Type:       types.UserCA,
		DomainName: rc.Secrets.SiteName,
	}, false)
	require.NoError(t, err)
	ca.SetRoles(nil) // Reset roles, otherwise they will take precedence.
	ca.SetRoleMap(types.RoleMap{{Remote: role.GetName(), Local: []string{role.GetName()}}})
	err = lc.Process.GetAuthServer().UpsertCertAuthority(ca)
	require.NoError(t, err)

	// Run command in root.
	outputRoot, err := runCommand(t, rc, []string{"echo", "hello root"}, helpers.ClientConfig{
		Login:   me.Username,
		Cluster: "root.example.com",
		Host:    Loopback,
		Port:    helpers.Port(t, rc.SSH),
	}, 1)
	require.NoError(t, err)
	require.Equal(t, "hello root", strings.TrimSpace(outputRoot))

	// Run command in leaf.
	outputLeaf, err := runCommand(t, rc, []string{"echo", "hello leaf"}, helpers.ClientConfig{
		Login:   me.Username,
		Cluster: "leaf.example.com",
		Host:    Loopback,
		Port:    helpers.Port(t, lc.SSH),
	}, 1)
	require.NoError(t, err)
	require.Equal(t, "hello leaf", strings.TrimSpace(outputLeaf))
}

// testSessionStreaming tests streaming events from session recordings.
func testSessionStreaming(t *testing.T, suite *integrationTestSuite) {
	ctx := context.Background()
	sessionID := session.ID(uuid.New().String())
	teleport := suite.newTeleport(t, nil, true)
	defer teleport.StopAll()

	api := teleport.GetSiteAPI(helpers.Site)
	uploadStream, err := api.CreateAuditStream(ctx, sessionID)
	require.Nil(t, err)

	generatedSession := events.GenerateTestSession(events.SessionParams{
		PrintEvents: 100,
		SessionID:   string(sessionID),
		ServerID:    "00000000-0000-0000-0000-000000000000",
	})

	for _, event := range generatedSession {
		err := uploadStream.EmitAuditEvent(ctx, event)
		require.NoError(t, err)
	}

	err = uploadStream.Complete(ctx)
	require.Nil(t, err)
	start := time.Now()

	// retry in case of error
outer:
	for time.Since(start) < time.Minute*5 {
		time.Sleep(time.Second * 5)

		receivedSession := make([]apievents.AuditEvent, 0)
		sessionPlayback, e := api.StreamSessionEvents(ctx, sessionID, 0)

	inner:
		for {
			select {
			case event, more := <-sessionPlayback:
				if !more {
					break inner
				}

				receivedSession = append(receivedSession, event)
			case <-ctx.Done():
				require.Nil(t, ctx.Err())
			case err := <-e:
				require.Nil(t, err)
			case <-time.After(time.Minute * 5):
				t.FailNow()
			}
		}

		for i := range generatedSession {
			receivedSession[i].SetClusterName("")
			if !reflect.DeepEqual(generatedSession[i], receivedSession[i]) {
				continue outer
			}
		}

		return
	}

	t.FailNow()
}

// TestKubeAgentFiltering tests that kube-agent filtering for pre-v8 agents and
// moderated sessions users works as expected.
func testKubeAgentFiltering(t *testing.T, suite *integrationTestSuite) {
	ctx := context.Background()

	type testCase struct {
		name     string
		server   types.Server
		role     types.Role
		user     types.User
		wantsLen int
	}

	v8Agent, err := types.NewServer("kube-h", types.KindKubeService, types.ServerSpecV2{
		Version:            "8.0.0",
		KubernetesClusters: []*types.KubernetesCluster{{Name: "foo"}},
	})
	require.NoError(t, err)

	v9Agent, err := types.NewServer("kube-h", types.KindKubeService, types.ServerSpecV2{
		Version:            "9.0.0",
		KubernetesClusters: []*types.KubernetesCluster{{Name: "foo"}},
	})
	require.NoError(t, err)

	plainRole, err := types.NewRole("plain", types.RoleSpecV5{})
	require.NoError(t, err)

	moderatedRole, err := types.NewRole("moderated", types.RoleSpecV5{
		Allow: types.RoleConditions{
			RequireSessionJoin: []*types.SessionRequirePolicy{
				{
					Name:  "bar",
					Kinds: []string{string(types.KubernetesSessionKind)},
				},
			},
		},
	})
	require.NoError(t, err)

	plainUser, err := types.NewUser("bob")
	require.NoError(t, err)
	plainUser.SetRoles([]string{plainRole.GetName()})

	moderatedUser, err := types.NewUser("alice")
	require.NoError(t, err)
	moderatedUser.SetRoles([]string{moderatedRole.GetName()})

	testCases := []testCase{
		{
			name:     "unrestricted user, v8 agent",
			server:   v8Agent,
			role:     plainRole,
			user:     plainUser,
			wantsLen: 1,
		},
		{
			name:     "restricted user, v8 agent",
			server:   v8Agent,
			role:     moderatedRole,
			user:     moderatedUser,
			wantsLen: 0,
		},
		{
			name:     "unrestricted user, v9 agent",
			server:   v9Agent,
			role:     plainRole,
			user:     plainUser,
			wantsLen: 1,
		},
		{
			name:     "restricted user, v9 agent",
			server:   v9Agent,
			role:     moderatedRole,
			user:     moderatedUser,
			wantsLen: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			teleport := suite.newTeleport(t, nil, true)
			defer teleport.StopAll()

			adminSite := teleport.Process.GetAuthServer()
			_, err := adminSite.UpsertKubeServiceV2(ctx, testCase.server)
			require.NoError(t, err)
			err = adminSite.UpsertRole(ctx, testCase.role)
			require.NoError(t, err)
			err = adminSite.CreateUser(ctx, testCase.user)
			require.NoError(t, err)

			cl, err := teleport.NewClient(helpers.ClientConfig{
				Login:   testCase.user.GetName(),
				Cluster: helpers.Site,
				Host:    Host,
				Port:    helpers.Port(t, teleport.SSH),
			})
			require.NoError(t, err)

			proxy, err := cl.ConnectToProxy(ctx)
			require.NoError(t, err)

			userSite, err := proxy.ConnectToCluster(ctx, helpers.Site)
			require.NoError(t, err)

			services, err := userSite.GetKubeServices(ctx)
			require.NoError(t, err)
			require.Len(t, services, testCase.wantsLen)
		})
	}
}

func createTrustedClusterPair(t *testing.T, suite *integrationTestSuite, extraServices func(*testing.T, *helpers.TeleInstance, *helpers.TeleInstance)) *client.TeleportClient {
	ctx := context.Background()
	username := suite.Me.Username
	name := "test"
	rootName := fmt.Sprintf("root-%s", name)
	leafName := fmt.Sprintf("leaf-%s", name)

	// Create root and leaf clusters.
	rootCfg := helpers.InstanceConfig{
		ClusterName: rootName,
		HostID:      helpers.HostID,
		NodeName:    Host,
		Priv:        suite.Priv,
		Pub:         suite.Pub,
		Log:         suite.Log,
	}
	rootCfg.Listeners = standardPortsOrMuxSetup(t, false, &rootCfg.Fds)
	root := helpers.NewInstance(t, rootCfg)

	leafCfg := helpers.InstanceConfig{
		ClusterName: leafName,
		HostID:      helpers.HostID,
		NodeName:    Host,
		Priv:        suite.Priv,
		Pub:         suite.Pub,
		Log:         suite.Log,
	}
	leafCfg.Listeners = standardPortsOrMuxSetup(t, false, &leafCfg.Fds)
	leaf := helpers.NewInstance(t, leafCfg)

	role, err := types.NewRoleV3("dev", types.RoleSpecV5{
		Allow: types.RoleConditions{
			Logins: []string{username},
		},
	})
	require.NoError(t, err)
	root.AddUserWithRole(username, role)

	makeConfig := func() (*testing.T, []*helpers.InstanceSecrets, *service.Config) {
		tconf := suite.defaultServiceConfig()
		tconf.Proxy.DisableWebService = false
		tconf.Proxy.DisableWebInterface = true
		tconf.SSH.Enabled = false
		return t, nil, tconf
	}

	oldInsecure := lib.IsInsecureDevMode()
	lib.SetInsecureDevMode(true)
	defer lib.SetInsecureDevMode(oldInsecure)

	require.NoError(t, root.CreateEx(makeConfig()))
	require.NoError(t, leaf.CreateEx(makeConfig()))
	require.NoError(t, leaf.Process.GetAuthServer().UpsertRole(ctx, role))

	// Connect leaf to root.
	tcToken := "trusted-cluster-token"
	tokenResource, err := types.NewProvisionToken(tcToken, []types.SystemRole{types.RoleTrustedCluster}, time.Time{})
	require.NoError(t, err)
	require.NoError(t, root.Process.GetAuthServer().UpsertToken(ctx, tokenResource))
	trustedCluster := root.AsTrustedCluster(tcToken, types.RoleMap{
		{Remote: "dev", Local: []string{"dev"}},
	})

	require.NoError(t, root.Start())
	t.Cleanup(func() { root.StopAll() })

	require.NoError(t, leaf.Start())
	t.Cleanup(func() { leaf.StopAll() })

	require.NoError(t, trustedCluster.CheckAndSetDefaults())
	helpers.TryCreateTrustedCluster(t, leaf.Process.GetAuthServer(), trustedCluster)
	helpers.WaitForTunnelConnections(t, root.Process.GetAuthServer(), leafName, 1)

	_, _, rootProxySSHPort := root.StartNodeAndProxy(t, "root-zero")
	_, _, _ = leaf.StartNodeAndProxy(t, "leaf-zero")

	// Add any extra services.
	if extraServices != nil {
		extraServices(t, root, leaf)
	}

	require.Eventually(t, helpers.WaitForClusters(root.Tunnel, 1), 10*time.Second, 1*time.Second)
	require.Eventually(t, helpers.WaitForClusters(leaf.Tunnel, 1), 10*time.Second, 1*time.Second)

	// Create client.
	creds, err := helpers.GenerateUserCreds(helpers.UserCredsRequest{
		Process:        root.Process,
		Username:       username,
		RouteToCluster: rootName,
	})
	require.NoError(t, err)

	tc, err := root.NewClientWithCreds(helpers.ClientConfig{
		Login:   username,
		Cluster: rootName,
		Host:    Loopback,
		Port:    rootProxySSHPort,
	}, *creds)
	require.NoError(t, err)

	leafCAs, err := leaf.Secrets.GetCAs()
	require.NoError(t, err)
	for _, leafCA := range leafCAs {
		require.NoError(t, tc.AddTrustedCA(context.Background(), leafCA))
	}

	return tc
}

func testListResourcesAcrossClusters(t *testing.T, suite *integrationTestSuite) {
	tc := createTrustedClusterPair(t, suite, func(t *testing.T, root, leaf *helpers.TeleInstance) {
		rootNodes := []string{"root-one", "root-two"}
		leafNodes := []string{"leaf-one", "leaf-two"}

		// Start a Teleport node that has SSH, Apps, Databases, and Kubernetes.
		startNode := func(name string, i *helpers.TeleInstance) {
			conf := suite.defaultServiceConfig()
			conf.Auth.Enabled = false
			conf.Proxy.Enabled = false

			conf.DataDir = t.TempDir()
			conf.SetToken("token")
			conf.UploadEventsC = i.UploadEventsC
			conf.SetAuthServerAddress(*utils.MustParseAddr(net.JoinHostPort(i.Hostname, helpers.PortStr(t, i.Web))))
			conf.HostUUID = name
			conf.Hostname = name
			conf.SSH.Enabled = true
			conf.CachePolicy = service.CachePolicy{
				Enabled: true,
			}
			conf.SSH.Addr = utils.NetAddr{
				Addr: helpers.NewListenerOn(t, Host, service.ListenerNodeSSH, &conf.FileDescriptors),
			}
			conf.Proxy.Enabled = false

			conf.Apps.Enabled = true
			conf.Apps.Apps = []service.App{
				{
					Name: name,
					URI:  name,
				},
			}

			conf.Databases.Enabled = true
			conf.Databases.Databases = []service.Database{
				{
					Name:     name,
					URI:      name,
					Protocol: "postgres",
				},
			}

			conf.Kube.KubeconfigPath = filepath.Join(conf.DataDir, "kube_config")
			require.NoError(t, helpers.EnableKube(t, conf, name))
			conf.Kube.ListenAddr = nil
			process, err := service.NewTeleport(conf)
			require.NoError(t, err)
			i.Nodes = append(i.Nodes, process)

			expectedEvents := []string{
				service.NodeSSHReady,
				service.AppsReady,
				service.DatabasesIdentityEvent,
				service.DatabasesReady,
				service.KubeIdentityEvent,
				service.KubernetesReady,
				service.TeleportReadyEvent,
			}

			receivedEvents, err := helpers.StartAndWait(process, expectedEvents)
			require.NoError(t, err)
			log.Debugf("Teleport Kube Server (in instance %v) started: %v/%v events received.",
				i.Secrets.SiteName, len(expectedEvents), len(receivedEvents))
		}

		for _, node := range rootNodes {
			startNode(node, root)
		}
		for _, node := range leafNodes {
			startNode(node, leaf)
		}
	})

	nodeTests := []struct {
		name     string
		search   string
		expected []string
	}{
		{
			name: "all nodes",
			expected: []string{
				"root-zero", "root-one", "root-two",
				"leaf-zero", "leaf-one", "leaf-two",
			},
		},
		{
			name:     "leaf only",
			search:   "leaf",
			expected: []string{"leaf-zero", "leaf-one", "leaf-two"},
		},
		{
			name:     "two only",
			search:   "two",
			expected: []string{"root-two", "leaf-two"},
		},
	}

	for _, test := range nodeTests {
		t.Run("node - "+test.name, func(t *testing.T) {
			if test.search != "" {
				tc.SearchKeywords = strings.Split(test.search, " ")
			} else {
				tc.SearchKeywords = nil
			}
			clusters, err := tc.ListNodesWithFiltersAllClusters(context.TODO())
			require.NoError(t, err)
			nodes := make([]string, 0)
			for _, v := range clusters {
				for _, node := range v {
					nodes = append(nodes, node.GetHostname())
				}
			}

			require.ElementsMatch(t, test.expected, nodes)
		})
	}

	// Everything other than ssh nodes.
	tests := []struct {
		name     string
		search   string
		expected []string
	}{
		{
			name: "all",
			expected: []string{
				"root-one", "root-two",
				"leaf-one", "leaf-two",
			},
		},
		{
			name:     "leaf only",
			search:   "leaf",
			expected: []string{"leaf-one", "leaf-two"},
		},
		{
			name:     "two only",
			search:   "two",
			expected: []string{"root-two", "leaf-two"},
		},
	}

	for _, test := range tests {
		if test.search != "" {
			tc.SearchKeywords = strings.Split(test.search, " ")
		} else {
			tc.SearchKeywords = nil
		}

		t.Run("apps - "+test.name, func(t *testing.T) {
			clusters, err := tc.ListAppsAllClusters(context.TODO(), nil)
			require.NoError(t, err)
			apps := make([]string, 0)
			for _, v := range clusters {
				for _, app := range v {
					apps = append(apps, app.GetName())
				}
			}

			require.ElementsMatch(t, test.expected, apps)
		})

		t.Run("databases - "+test.name, func(t *testing.T) {
			clusters, err := tc.ListDatabasesAllClusters(context.TODO(), nil)
			require.NoError(t, err)
			databases := make([]string, 0)
			for _, v := range clusters {
				for _, db := range v {
					databases = append(databases, db.GetName())
				}
			}

			require.ElementsMatch(t, test.expected, databases)
		})

		t.Run("kube - "+test.name, func(t *testing.T) {
			req := proto.ListResourcesRequest{}
			if test.search != "" {
				req.SearchKeywords = strings.Split(test.search, " ")
			}
			clusterMap, err := tc.ListKubernetesClustersWithFiltersAllClusters(context.TODO(), req)
			require.NoError(t, err)
			clusters := make([]string, 0)
			for _, cl := range clusterMap {
				for _, c := range cl {
					clusters = append(clusters, c.GetName())
				}
			}
			require.ElementsMatch(t, test.expected, clusters)
		})
	}
}

func testJoinOverReverseTunnelOnly(t *testing.T, suite *integrationTestSuite) {
	lib.SetInsecureDevMode(true)
	defer lib.SetInsecureDevMode(false)

	// Create a Teleport instance with Auth/Proxy.
	mainConfig := suite.defaultServiceConfig()
	mainConfig.Auth.Enabled = true

	mainConfig.Proxy.Enabled = true
	mainConfig.Proxy.DisableWebService = false
	mainConfig.Proxy.DisableWebInterface = true

	mainConfig.SSH.Enabled = false

	main := suite.NewTeleportWithConfig(t, nil, nil, mainConfig)
	t.Cleanup(func() { require.NoError(t, main.StopAll()) })

	// Create a Teleport instance with a Node.
	nodeConfig := suite.defaultServiceConfig()
	nodeConfig.Hostname = Host
	nodeConfig.SetToken("token")

	nodeConfig.Auth.Enabled = false
	nodeConfig.Proxy.Enabled = false
	nodeConfig.SSH.Enabled = true

	_, err := main.StartNodeWithTargetPort(nodeConfig, helpers.PortStr(t, main.ReverseTunnel))
	require.NoError(t, err, "Node failed to join over reverse tunnel")
}

func testSFTP(t *testing.T, suite *integrationTestSuite) {
	// Create Teleport instance.
	teleport := suite.newTeleport(t, nil, true)
	t.Cleanup(func() {
		teleport.StopAll()
	})

	client, err := teleport.NewClient(helpers.ClientConfig{
		Login:   suite.Me.Username,
		Cluster: helpers.Site,
		Host:    Host,
	})
	require.NoError(t, err)

	// Create SFTP session.
	ctx := context.Background()
	proxyClient, err := client.ConnectToProxy(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		proxyClient.Close()
	})

	sftpClient, err := sftp.NewClient(proxyClient.Client.Client)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sftpClient.Close())
	})

	// Create file that will be uploaded and downloaded.
	tempDir := t.TempDir()
	testFilePath := filepath.Join(tempDir, "testfile")
	testFile, err := os.Create(testFilePath)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, testFile.Close())
	})

	_, err = testFile.WriteString("This is test data.")
	require.NoError(t, err)
	require.NoError(t, testFile.Sync())

	// Test stat'ing a file.
	t.Run("stat", func(t *testing.T) {
		fi, err := sftpClient.Stat(testFilePath)
		require.NoError(t, err)
		require.NotNil(t, fi)
	})

	// Test downloading a file.
	t.Run("download", func(t *testing.T) {
		testFileDownload := testFilePath + "-download"
		downloadFile, err := os.Create(testFileDownload)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, downloadFile.Close())
		})

		remoteDownloadFile, err := sftpClient.Open(testFilePath)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, remoteDownloadFile.Close())
		})

		_, err = io.Copy(downloadFile, remoteDownloadFile)
		require.NoError(t, err)
	})

	// Test uploading a file.
	t.Run("upload", func(t *testing.T) {
		testFileUpload := testFilePath + "-upload"
		remoteUploadFile, err := sftpClient.Create(testFileUpload)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, remoteUploadFile.Close())
		})

		_, err = io.Copy(remoteUploadFile, testFile)
		require.NoError(t, err)
	})

	t.Run("chmod", func(t *testing.T) {
		err = sftpClient.Chmod(testFilePath, 0o777)
		require.NoError(t, err)
	})

	// Ensure SFTP audit events are present.
	sftpEvent, err := findEventInLog(teleport, events.SFTPEvent)
	require.NoError(t, err)
	require.Equal(t, testFilePath, sftpEvent.GetString(events.SFTPPath))
}
