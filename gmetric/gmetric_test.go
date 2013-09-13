package gmetric_test

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"testing"
	"text/template"
	"time"

	"github.com/daaku/go.freeport"

	"github.com/daaku/go.ganglia/gmetric"
	"github.com/daaku/go.ganglia/gmon"
)

const localhostIP = "127.0.0.1"

var configTemplate, configTemplateErr = template.New("config").Parse(`
globals {
  daemonize = no
  setuid = false
  debug_level = 0
  max_udp_msg_len = 1472
  mute = no
  deaf = no
  allow_extra_data = yes
}

cluster {
  name = "gmetric_test"
  owner = "gmetric_test"
  latlong = "gmetric_test"
  url = "gmetric_test"
}

host {
  location = "gmetric_test"
}

udp_recv_channel {
  port = {{.Port}}
  family = inet4
}

udp_recv_channel {
  port = {{.Port}}
  family = inet6
}

tcp_accept_channel {
  port = {{.Port}}
}
`)

func init() {
	if configTemplateErr != nil {
		panic(configTemplateErr)
	}
}

type harness struct {
	Client     *gmetric.Client
	Port       int
	T          *testing.T
	ConfigPath string
	Cmd        *exec.Cmd
}

func (h *harness) Start() {
	var err error
	if h.Port == 0 {
		if h.Port, err = freeport.Get(); err != nil {
			h.T.Fatal(err)
		}
	}

	cf, err := ioutil.TempFile("", "gmetric_test_gmond_conf")
	if err != nil {
		h.T.Fatal(err)
	}
	h.ConfigPath = cf.Name()

	if err := configTemplate.Execute(cf, h); err != nil {
		h.T.Fatal(err)
	}

	if err := cf.Close(); err != nil {
		h.T.Fatal(err)
	}

	h.Cmd = exec.Command("gmond", "--conf", h.ConfigPath)
	h.Cmd.Stderr = os.Stderr
	h.Cmd.Stdout = os.Stdout
	if err := h.Cmd.Start(); err != nil {
		h.T.Fatal(err)
	}

	// Wait until TCP socket is active to ensure we don't progress until the
	// server is ready to accept.
	for {
		if c, err := net.Dial("tcp", fmt.Sprintf("%s:%d", localhostIP, h.Port)); err == nil {
			c.Close()
			break
		}
	}

	h.Client = &gmetric.Client{
		Addr: []*net.UDPAddr{
			&net.UDPAddr{IP: net.ParseIP(localhostIP), Port: h.Port},
		},
	}

	if err := h.Client.Start(); err != nil {
		h.T.Fatal(err)
	}
}

func (h *harness) Stop() {
	if err := h.Client.Stop(); err != nil {
		h.T.Fatal(err)
	}

	if err := h.Cmd.Process.Kill(); err != nil {
		h.T.Fatal(err)
	}

	if err := os.Remove(h.ConfigPath); err != nil {
		h.T.Fatal(err)
	}
}

func (h *harness) State() *gmon.Ganglia {
	addr := fmt.Sprintf("%s:%d", localhostIP, h.Port)
	ganglia, err := gmon.RemoteRead("tcp", addr)
	if err != nil {
		h.T.Fatal(err)
	}
	return ganglia
}

func (h *harness) ContainsMetric(m *gmon.Metric) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		g := h.State()
		for _, cluster := range g.Clusters {
			for _, host := range cluster.Hosts {
				for _, metric := range host.Metrics {
					if metric.Name == m.Name {
						return
					}
				}
			}
		}

		if time.Now().After(deadline) {
			h.T.Fatalf("did not find metric %v in\n%v", m, g)
		}
	}
}

func newHarness(t *testing.T) *harness {
	h := &harness{T: t}
	h.Start()
	return h
}

func TestSimpleMetric(t *testing.T) {
	h := newHarness(t)
	defer h.Stop()

	m := &gmetric.Metric{
		Name:         "simple_metric",
		Host:         "localhost",
		ValueType:    gmetric.ValueUint32,
		Units:        "count",
		Slope:        gmetric.SlopeBoth,
		TickInterval: 20 * time.Second,
		Lifetime:     24 * time.Hour,
	}
	const val = 10

	if err := h.Client.SendMeta(m); err != nil {
		t.Fatal(err)
	}

	if err := h.Client.SendValue(m, val); err != nil {
		t.Fatal(err)
	}

	h.ContainsMetric(&gmon.Metric{
		Name:  m.Name,
		Value: val,
		Unit:  m.Units,
		Tmax:  20,
		Dmax:  86400,
		Slope: "both",
	})
}