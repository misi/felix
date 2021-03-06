// Copyright (c) 2020 Tigera, Inc. All rights reserved.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ut_test

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/felix/bpf"
	"github.com/projectcalico/felix/bpf/asm"
	"github.com/projectcalico/felix/bpf/ipsets"
	"github.com/projectcalico/felix/bpf/polprog"
	"github.com/projectcalico/felix/bpf/state"
	"github.com/projectcalico/felix/idalloc"
	"github.com/projectcalico/felix/proto"
)

func TestLoadAllowAllProgram(t *testing.T) {
	RegisterTestingT(t)

	b := asm.NewBlock()
	b.MovImm32(asm.R0, -1)
	b.Exit()
	insns, err := b.Assemble()
	Expect(err).NotTo(HaveOccurred())

	fd, err := bpf.LoadBPFProgramFromInsns(insns, "Apache-2.0")
	Expect(err).NotTo(HaveOccurred())
	Expect(fd).NotTo(BeZero())
	defer func() {
		Expect(fd.Close()).NotTo(HaveOccurred())
	}()

	rc, err := bpf.RunBPFProgram(fd, make([]byte, 500), 1)
	Expect(err).NotTo(HaveOccurred())
	Expect(rc.RC).To(BeNumerically("==", -1))
}

func TestLoadProgramWithMapAcccess(t *testing.T) {
	RegisterTestingT(t)

	ipsMap := ipsets.Map(&bpf.MapContext{})
	Expect(ipsMap.EnsureExists()).NotTo(HaveOccurred())
	Expect(ipsMap.MapFD()).NotTo(BeZero())

	b := asm.NewBlock()
	b.MovImm64(asm.R1, 0)
	b.StoreStack64(asm.R1, -8)
	b.StoreStack64(asm.R1, -16)
	b.StoreStack64(asm.R1, -24)
	b.StoreStack64(asm.R1, -32)
	b.Mov64(asm.R2, asm.R10)
	b.AddImm64(asm.R2, -32)
	b.LoadMapFD(asm.R1, uint32(ipsMap.MapFD()))
	b.Call(asm.HelperMapLookupElem)
	b.MovImm32(asm.R0, -1)
	b.Exit()
	insns, err := b.Assemble()
	Expect(err).NotTo(HaveOccurred())

	fd, err := bpf.LoadBPFProgramFromInsns(insns, "Apache-2.0")
	Expect(err).NotTo(HaveOccurred())
	Expect(fd).NotTo(BeZero())
	defer func() {
		Expect(fd.Close()).NotTo(HaveOccurred())
	}()

	rc, err := bpf.RunBPFProgram(fd, make([]byte, 500), 1)
	Expect(err).NotTo(HaveOccurred())
	Expect(rc.RC).To(BeNumerically("==", -1))
}

func TestLoadKitchenSinkPolicy(t *testing.T) {
	RegisterTestingT(t)
	alloc := idalloc.New()
	allocID := func(id string) string {
		alloc.GetOrAlloc(id)
		return id
	}

	cleanIPSetMap()

	pg := polprog.NewBuilder(alloc, ipsMap.MapFD(), stateMap.MapFD(), jumpMap.MapFD())
	insns, err := pg.Instructions([][][]*proto.Rule{{{{
		Action:                  "Allow",
		IpVersion:               4,
		Protocol:                &proto.Protocol{NumberOrName: &proto.Protocol_Number{Number: 6}},
		SrcNet:                  []string{"10.0.0.0/8"},
		SrcPorts:                []*proto.PortRange{{First: 80, Last: 81}, {First: 8080, Last: 8081}},
		SrcNamedPortIpSetIds:    []string{allocID("n:abcdef1234567890")},
		DstNet:                  []string{"11.0.0.0/8"},
		DstPorts:                []*proto.PortRange{{First: 3000, Last: 3001}},
		DstNamedPortIpSetIds:    []string{allocID("n:foo1234567890")},
		Icmp:                    nil,
		SrcIpSetIds:             []string{allocID("s:sbcdef1234567890")},
		DstIpSetIds:             []string{allocID("s:dbcdef1234567890")},
		NotProtocol:             &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "UDP"}},
		NotSrcNet:               []string{"12.0.0.0/8"},
		NotSrcPorts:             []*proto.PortRange{{First: 5000, Last: 5000}},
		NotDstNet:               []string{"13.0.0.0/8"},
		NotDstPorts:             []*proto.PortRange{{First: 4000, Last: 4000}},
		NotIcmp:                 nil,
		NotSrcIpSetIds:          []string{allocID("s:abcdef1234567890")},
		NotDstIpSetIds:          []string{allocID("s:abcdef123456789l")},
		NotSrcNamedPortIpSetIds: []string{allocID("n:0bcdef1234567890")},
		NotDstNamedPortIpSetIds: []string{allocID("n:0bcdef1234567890")},
	}}}})

	Expect(err).NotTo(HaveOccurred())
	fd, err := bpf.LoadBPFProgramFromInsns(insns, "Apache-2.0")
	Expect(err).NotTo(HaveOccurred())
	Expect(fd).NotTo(BeZero())
	Expect(fd.Close()).NotTo(HaveOccurred())
}

func TestLoadGarbageProgram(t *testing.T) {
	RegisterTestingT(t)

	var insns asm.Insns
	for i := 0; i < 256; i++ {
		i := uint8(i)
		insns = append(insns, asm.Insn{i, i, i, i, i, i, i, i})
	}

	fd, err := bpf.LoadBPFProgramFromInsns(insns, "Apache-2.0")
	Expect(err).To(HaveOccurred())
	Expect(fd).To(BeZero())
}

const (
	RCDrop            = 2
	RCEpilogueReached = 123
)

func packetWithPorts(proto int, src, dst string) packet {
	parts := strings.Split(src, ":")
	srcAddr := parts[0]
	srcPort, err := strconv.Atoi(parts[1])
	if err != nil {
		panic(err)
	}
	parts = strings.Split(dst, ":")
	dstAddr := parts[0]
	dstPort, err := strconv.Atoi(parts[1])
	if err != nil {
		panic(err)
	}
	return packet{
		protocol: proto,
		srcAddr:  srcAddr,
		srcPort:  srcPort,
		dstAddr:  dstAddr,
		dstPort:  dstPort,
	}
}

func tcpPkt(src, dst string) packet {
	return packetWithPorts(6, src, dst)
}

func udpPkt(src, dst string) packet {
	return packetWithPorts(17, src, dst)
}

func icmpPkt(src, dst string) packet {
	return packetWithPorts(1, src+":0", dst+":0")
}
func icmpPktWithTypeCode(src, dst string, icmpType, icmpCode int) packet {
	return packet{
		protocol: 1,
		srcAddr:  src,
		srcPort:  0,
		dstAddr:  dst,
		dstPort:  (icmpCode << 8) | (icmpType),
	}
}

var polProgramTests = []polProgramTest{
	// Tests of actions and flow control.
	{
		PolicyName: "no tiers",
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},
	{
		PolicyName: "unreachable tier",
		Policy: [][][]*proto.Rule{
			{},
			{{{
				Action: "Allow",
			}}},
		},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},
	{
		PolicyName: "pass to nowhere",
		Policy: [][][]*proto.Rule{
			{{{
				Action: "Pass",
			}}},
		},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},
	{
		PolicyName: "pass to allow",
		Policy: [][][]*proto.Rule{
			{
				{
					{Action: "Pass"},
					{Action: "Deny"},
				},
			},
			{
				{
					{Action: "Allow"},
				},
			},
		},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},
	{
		PolicyName: "pass to deny",
		Policy: [][][]*proto.Rule{
			{
				{
					{Action: "Pass"},
					{Action: "Allow"},
				},
			},
			{
				{
					{Action: "Deny"},
				},
			},
		},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},
	{
		PolicyName: "explicit allow",
		Policy: [][][]*proto.Rule{{{{
			Action: "Allow",
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},
	{
		PolicyName: "explicit deny",
		Policy: [][][]*proto.Rule{{{{
			Action: "Deny",
		}}}},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},

	// Protocol match tests.

	{
		PolicyName: "allow tcp",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
		DroppedPackets: []packet{
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},
	{
		PolicyName: "allow !tcp",
		Policy: [][][]*proto.Rule{{{{
			Action:      "Allow",
			NotProtocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
		}}}},
		AllowedPackets: []packet{
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
	},
	{
		PolicyName: "allow udp",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "udp"}},
		}}}},
		AllowedPackets: []packet{
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},

	// CIDR tests.

	{
		PolicyName: "allow 10.0.0.1/32",
		Policy: [][][]*proto.Rule{{{{
			Action: "Allow",
			SrcNet: []string{"10.0.0.1/32"},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245")},
	},
	{
		PolicyName: "allow from 10.0.0.0/8",
		Policy: [][][]*proto.Rule{{{{
			Action: "Allow",
			SrcNet: []string{"10.0.0.0/8"},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245")},
		DroppedPackets: []packet{
			icmpPkt("11.0.0.1", "10.0.0.2")},
	},
	{
		PolicyName: "allow from CIDRs",
		Policy: [][][]*proto.Rule{{{{
			Action: "Allow",
			SrcNet: []string{"102.0.0.0/8", "10.0.0.1/32", "11.0.0.1/32"},
		}}}},
		AllowedPackets: []packet{
			icmpPkt("11.0.0.1", "10.0.0.2"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
	},
	{
		PolicyName: "allow from !CIDRs",
		Policy: [][][]*proto.Rule{{{{
			Action:    "Allow",
			NotSrcNet: []string{"102.0.0.0/8", "10.0.0.1/32", "11.0.0.1/32"},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
		DroppedPackets: []packet{
			icmpPkt("11.0.0.1", "10.0.0.2"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80")},
	},
	{
		PolicyName: "allow to CIDRs",
		Policy: [][][]*proto.Rule{{{{
			Action: "Allow",
			DstNet: []string{"102.0.0.0/8", "10.0.0.1/32", "11.0.0.1/32"},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
		DroppedPackets: []packet{
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024")},
	},
	{
		PolicyName: "allow to !CIDRs",
		Policy: [][][]*proto.Rule{{{{
			Action:    "Allow",
			NotDstNet: []string{"102.0.0.0/8", "10.0.0.1/32", "11.0.0.1/32"},
		}}}},
		AllowedPackets: []packet{
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
	},
	{
		PolicyName: "allow from !10.0.0.0/8",
		Policy: [][][]*proto.Rule{{{{
			Action:    "Allow",
			NotSrcNet: []string{"10.0.0.0/8"},
		}}}},
		AllowedPackets: []packet{
			icmpPkt("11.0.0.1", "10.0.0.2")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245")},
	},
	{
		PolicyName: "allow to 10.0.0.1/32",
		Policy: [][][]*proto.Rule{{{{
			Action: "Allow",
			DstNet: []string{"10.0.0.1/32"},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2"),
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024")},
	},
	{
		PolicyName: "allow to 10.0.0.0/8",
		Policy: [][][]*proto.Rule{{{{
			Action: "Allow",
			DstNet: []string{"10.0.0.0/8"},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			icmpPkt("11.0.0.1", "10.0.0.2")},
		DroppedPackets: []packet{
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024")},
	},
	{
		PolicyName: "allow to !10.0.0.0/8",
		Policy: [][][]*proto.Rule{{{{
			Action:    "Allow",
			NotDstNet: []string{"10.0.0.0/8"},
		}}}},
		AllowedPackets: []packet{
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245")},
	},

	// Port tests.

	{
		PolicyName: "allow from tcp:80",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			SrcPorts: []*proto.PortRange{{
				First: 80,
				Last:  80,
			}},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},
	{
		PolicyName: "allow from tcp:80-81",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			SrcPorts: []*proto.PortRange{{
				First: 80,
				Last:  81,
			}},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			tcpPkt("10.0.0.2:81", "10.0.0.1:31245")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:79", "10.0.0.1:31245"),
			tcpPkt("10.0.0.2:82", "10.0.0.1:31245"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245")},
	},
	{
		PolicyName: "allow from tcp:0-80",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			SrcPorts: []*proto.PortRange{{
				First: 0,
				Last:  80,
			}},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.2:0", "10.0.0.1:31245"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:81", "10.0.0.1:31245")},
	},
	{
		PolicyName: "allow to tcp:80-65535",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			DstPorts: []*proto.PortRange{{
				First: 80,
				Last:  65535,
			}},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:65535")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:79")},
	},
	{
		PolicyName: "allow to tcp:ranges",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			DstPorts: []*proto.PortRange{
				{First: 80, Last: 81},
				{First: 90, Last: 90},
			},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:81"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:90")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:79"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:82"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:89"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:91"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80")},
	},
	{
		PolicyName: "allow to tcp:!ranges",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			NotDstPorts: []*proto.PortRange{
				{First: 80, Last: 81},
				{First: 90, Last: 90},
			},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:79"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:82"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:89"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:91")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:81"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:90"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80")},
	},
	{
		PolicyName: "allow from tcp:!80",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			NotSrcPorts: []*proto.PortRange{{
				First: 80,
				Last:  80,
			}},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},
	{
		PolicyName: "allow to tcp:80",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			DstPorts: []*proto.PortRange{{
				First: 80,
				Last:  80,
			}},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},
	{
		// BPF immediate values are signed, check that we don't get tripped up by a sign extension.
		PolicyName: "allow to tcp:65535",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			DstPorts: []*proto.PortRange{{
				First: 65535,
				Last:  65535,
			}},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:65535")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
	},
	{
		PolicyName: "allow to tcp:!80",
		Policy: [][][]*proto.Rule{{{{
			Action:   "Allow",
			Protocol: &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			NotDstPorts: []*proto.PortRange{{
				First: 80,
				Last:  80,
			}},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
	},

	// IP set tests.
	{
		PolicyName: "allow from empty IP set",
		Policy: [][][]*proto.Rule{{{{
			Action:      "Allow",
			Protocol:    &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			SrcIpSetIds: []string{"setA"},
		}}}},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
		IPSets: map[string][]string{
			"setA": {},
		},
	},
	{
		PolicyName: "allow from !empty IP set",
		Policy: [][][]*proto.Rule{{{{
			Action:         "Allow",
			Protocol:       &proto.Protocol{NumberOrName: &proto.Protocol_Name{Name: "tcp"}},
			NotSrcIpSetIds: []string{"setA"},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80")},
		DroppedPackets: []packet{
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
		IPSets: map[string][]string{
			"setA": {},
		},
	},
	{
		PolicyName: "allow from IP set",
		Policy: [][][]*proto.Rule{{{{
			Action:      "Allow",
			SrcIpSetIds: []string{"setA"},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
		DroppedPackets: []packet{
			tcpPkt("11.0.0.1:12345", "10.0.0.2:8080")},
		IPSets: map[string][]string{
			"setA": {"10.0.0.0/8"},
		},
	},
	{
		PolicyName: "allow to IP set",
		Policy: [][][]*proto.Rule{{{{
			Action:      "Allow",
			DstIpSetIds: []string{"setA"},
		}}}},
		AllowedPackets: []packet{
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024")},
		DroppedPackets: []packet{
			tcpPkt("11.0.0.1:12345", "10.0.0.2:8080"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80")},
		IPSets: map[string][]string{
			"setA": {"11.0.0.0/8", "123.0.0.1/32"},
		},
	},
	{
		PolicyName: "allow from !IP set",
		Policy: [][][]*proto.Rule{{{{
			Action:         "Allow",
			NotSrcIpSetIds: []string{"setA"},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("11.0.0.1:12345", "10.0.0.2:8080")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024"),
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			icmpPkt("10.0.0.1", "10.0.0.2")},
		IPSets: map[string][]string{
			"setA": {"10.0.0.0/8"},
		},
	},
	{
		PolicyName: "allow to !IP set",
		Policy: [][][]*proto.Rule{{{{
			Action:         "Allow",
			NotDstIpSetIds: []string{"setA"},
		}}}},
		AllowedPackets: []packet{
			tcpPkt("11.0.0.1:12345", "10.0.0.2:8080"),
			udpPkt("10.0.0.1:31245", "10.0.0.2:80")},
		DroppedPackets: []packet{
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024")},
		IPSets: map[string][]string{
			"setA": {"11.0.0.0/8", "123.0.0.1/32"},
		},
	},
	{
		PolicyName: "allow to named port",
		Policy: [][][]*proto.Rule{{{{
			Action:               "Allow",
			DstNamedPortIpSetIds: []string{"setA"},
		}}}},
		AllowedPackets: []packet{
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80")},
		DroppedPackets: []packet{
			tcpPkt("11.0.0.1:12345", "10.0.0.2:8080"), // Wrong port
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),   // Wrong proto
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),   // Src/dest confusion
			tcpPkt("10.0.0.2:31245", "10.0.0.1:80"),   // Wrong dest
		},
		IPSets: map[string][]string{
			"setA": {"10.0.0.2/32,tcp:80", "123.0.0.1/32,udp:1024"},
		},
	},
	{
		PolicyName: "allow to named ports",
		Policy: [][][]*proto.Rule{{{{
			Action:               "Allow",
			DstNamedPortIpSetIds: []string{"setA", "setB"},
		}}}},
		AllowedPackets: []packet{
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80")},
		DroppedPackets: []packet{
			tcpPkt("11.0.0.1:12345", "10.0.0.2:8080"), // Wrong port
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),   // Wrong proto
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),   // Src/dest confusion
			tcpPkt("10.0.0.2:31245", "10.0.0.1:80"),   // Wrong dest
		},
		IPSets: map[string][]string{
			"setA": {"10.0.0.2/32,tcp:80"},
			"setB": {"123.0.0.1/32,udp:1024"},
		},
	},
	{
		PolicyName: "allow to mixed ports",
		Policy: [][][]*proto.Rule{{{{
			Action: "Allow",
			// Should match either port or named port
			DstPorts: []*proto.PortRange{
				{First: 81, Last: 82},
				{First: 90, Last: 90},
			},
			DstNamedPortIpSetIds: []string{"setA", "setB"},
		}}}},
		AllowedPackets: []packet{
			udpPkt("10.0.0.2:12345", "123.0.0.1:1024"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:90"),
			tcpPkt("10.0.0.1:31245", "10.0.0.2:82")},
		DroppedPackets: []packet{
			tcpPkt("11.0.0.1:12345", "10.0.0.2:8080"), // Wrong port
			udpPkt("10.0.0.1:31245", "10.0.0.2:80"),   // Wrong proto
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245"),   // Src/dest confusion
			tcpPkt("10.0.0.2:31245", "10.0.0.1:80"),   // Wrong dest
		},
		IPSets: map[string][]string{
			"setA": {"10.0.0.2/32,tcp:80"},
			"setB": {"123.0.0.1/32,udp:1024"},
		},
	},
	{
		PolicyName: "allow from named port",
		Policy: [][][]*proto.Rule{{{{
			Action:               "Allow",
			SrcNamedPortIpSetIds: []string{"setA"},
		}}}},
		AllowedPackets: []packet{
			udpPkt("123.0.0.1:1024", "10.0.0.2:12345"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:8080", "11.0.0.1:12345"), // Wrong port
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),   // Wrong proto
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),   // Src/dest confusion
			tcpPkt("10.0.0.1:80", "10.0.0.2:31245"),   // Wrong src
		},
		IPSets: map[string][]string{
			"setA": {"10.0.0.2/32,tcp:80", "123.0.0.1/32,udp:1024"},
		},
	},
	{
		PolicyName: "allow from named ports",
		Policy: [][][]*proto.Rule{{{{
			Action:               "Allow",
			SrcNamedPortIpSetIds: []string{"setA", "setB"},
		}}}},
		AllowedPackets: []packet{
			udpPkt("123.0.0.1:1024", "10.0.0.2:12345"),
			tcpPkt("10.0.0.2:80", "10.0.0.1:31245")},
		DroppedPackets: []packet{
			tcpPkt("10.0.0.2:8080", "11.0.0.1:12345"), // Wrong port
			udpPkt("10.0.0.2:80", "10.0.0.1:31245"),   // Wrong proto
			tcpPkt("10.0.0.1:31245", "10.0.0.2:80"),   // Src/dest confusion
			tcpPkt("10.0.0.1:80", "10.0.0.2:31245"),   // Wrong src
		},
		IPSets: map[string][]string{
			"setA": {"10.0.0.2/32,tcp:80"},
			"setB": {"123.0.0.1/32,udp:1024"},
		},
	},
	//ICMP tests
	{
		PolicyName: "allow icmp packet with type 8",
		Policy: [][][]*proto.Rule{{{{
			Action: "Allow",
			Icmp:   &proto.Rule_IcmpType{IcmpType: 8},
		}}}},
		AllowedPackets: []packet{
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 8, 0)},
		DroppedPackets: []packet{
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 10, 0)},
	},
	{
		PolicyName: "allow icmp packet with type 8 and code 3",
		Policy: [][][]*proto.Rule{{{{
			Action: "Allow",
			Icmp:   &proto.Rule_IcmpTypeCode{IcmpTypeCode: &proto.IcmpTypeAndCode{Type: 8, Code: 3}},
		}}}},
		AllowedPackets: []packet{
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 8, 3)},
		DroppedPackets: []packet{
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 10, 0),
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 10, 3),
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 8, 4)},
	},
	{
		PolicyName: "allow icmp packet with type not equal to 8",
		Policy: [][][]*proto.Rule{{{{
			Action:  "Allow",
			NotIcmp: &proto.Rule_NotIcmpType{NotIcmpType: 8},
		}}}},
		AllowedPackets: []packet{
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 10, 0)},
		DroppedPackets: []packet{
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 8, 0)},
	},
	{
		PolicyName: "allow icmp packet with type not equal to 8 and code not equal to 3",
		Policy: [][][]*proto.Rule{{{{
			Action:  "Allow",
			NotIcmp: &proto.Rule_NotIcmpTypeCode{NotIcmpTypeCode: &proto.IcmpTypeAndCode{Type: 8, Code: 3}},
		}}}},
		AllowedPackets: []packet{
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 10, 0),
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 8, 4),
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 10, 3)},
		DroppedPackets: []packet{
			icmpPktWithTypeCode("10.0.0.1", "10.0.0.2", 8, 3)},
	},
}

func TestPolicyPrograms(t *testing.T) {
	for i, p := range polProgramTests {
		t.Run(fmt.Sprintf("%d:Policy=%s", i, p.PolicyName), p.Run)
	}
}

type polProgramTest struct {
	PolicyName     string
	Policy         [][][]*proto.Rule
	AllowedPackets []packet
	DroppedPackets []packet
	IPSets         map[string][]string
}

type packet struct {
	protocol int
	srcAddr  string
	srcPort  int
	dstAddr  string
	dstPort  int
}

func (p packet) String() string {
	protoName := fmt.Sprint(p.protocol)
	switch p.protocol {
	case 6:
		protoName = "tcp"
	case 17:
		protoName = "udp"
	case 1:
		protoName = "icmp"
	}
	return fmt.Sprintf("%s-%s:%d->%s:%d", protoName, p.srcAddr, p.srcPort, p.dstAddr, p.dstPort)
}

func (p packet) ToState() state.State {
	if uint8(p.protocol) == 1 {
		return state.State{
			IPProto:        uint8(p.protocol),
			SrcAddr:        ipUintFromString(p.srcAddr),
			PostNATDstAddr: ipUintFromString(p.dstAddr),
			SrcPort:        uint16(p.srcPort),
			DstPort:        uint16(p.dstPort),
		}
	}
	return state.State{
		IPProto:        uint8(p.protocol),
		SrcAddr:        ipUintFromString(p.srcAddr),
		PostNATDstAddr: ipUintFromString(p.dstAddr),
		SrcPort:        uint16(p.srcPort),
		PostNATDstPort: uint16(p.dstPort),
	}
}

func ipUintFromString(addrStr string) uint32 {
	if addrStr == "" {
		return 0
	}
	addr := net.ParseIP(addrStr)
	return binary.LittleEndian.Uint32(addr.To4())
}

func TestIPUintFromString(t *testing.T) {
	RegisterTestingT(t)
	Expect(ipUintFromString("10.0.0.1")).To(Equal(uint32(0x0100000a)))
}

func (p *polProgramTest) Run(t *testing.T) {
	RegisterTestingT(t)

	// The prog builder refuses to allocate IDs as a precaution, give it an allocator that forces allocations.
	realAlloc := idalloc.New()
	forceAlloc := &forceAllocator{alloc: realAlloc}

	// MAke sure the maps are available.
	cleanIPSetMap()
	// FIXME should clean up the maps at the end of each test but recreating the maps seems to be racy

	p.setUpIPSets(realAlloc, ipsMap)

	// Build the program.
	pg := polprog.NewBuilder(forceAlloc, ipsMap.MapFD(), testStateMap.MapFD(), jumpMap.MapFD())
	insns, err := pg.Instructions(p.Policy)
	Expect(err).NotTo(HaveOccurred(), "failed to assemble program")

	// Load the program into the kernel.  We don't pin it so it'll be removed when the
	// test process exits (or by the defer).
	polProgFD, err := bpf.LoadBPFProgramFromInsns(insns, "Apache-2.0")
	Expect(err).NotTo(HaveOccurred(), "failed to load program into the kernel")
	Expect(polProgFD).NotTo(BeZero())
	defer func() {
		err := polProgFD.Close()
		Expect(err).NotTo(HaveOccurred())
	}()

	// Give the policy program somewhere to jump to.
	epiFD := p.installEpilogueProgram(jumpMap)
	defer func() {
		err := epiFD.Close()
		Expect(err).NotTo(HaveOccurred())
	}()

	log.Debug("Setting up state map")
	for _, pkt := range p.AllowedPackets {
		pkt := pkt
		t.Run(fmt.Sprintf("should allow %v", pkt), func(t *testing.T) {
			RegisterTestingT(t)
			p.runProgram(pkt.ToState(), testStateMap, polProgFD, RCEpilogueReached, polprog.PolRCAllow)
		})
	}
	for _, pkt := range p.DroppedPackets {
		pkt := pkt
		t.Run(fmt.Sprintf("should drop %v", pkt), func(t *testing.T) {
			RegisterTestingT(t)
			p.runProgram(pkt.ToState(), testStateMap, polProgFD, RCDrop, polprog.PolRCNoMatch)
		})
	}
}

// installEpilogueProgram installs a trivial BPF program into the jump table that returns RCEpilogueReached.
func (p *polProgramTest) installEpilogueProgram(jumpMap bpf.Map) bpf.ProgFD {
	b := asm.NewBlock()

	// Load the RC into the return register.
	b.MovImm64(asm.R0, RCEpilogueReached)
	// Exit!
	b.Exit()

	epiInsns, err := b.Assemble()
	Expect(err).NotTo(HaveOccurred())
	epiFD, err := bpf.LoadBPFProgramFromInsns(epiInsns, "Apache-2.0")
	Expect(err).NotTo(HaveOccurred(), "failed to load program into the kernel")
	Expect(epiFD).NotTo(BeZero())

	jumpValue := make([]byte, 4)
	binary.LittleEndian.PutUint32(jumpValue, uint32(epiFD))
	err = jumpMap.Update([]byte{1, 0, 0, 0}, jumpValue)
	Expect(err).NotTo(HaveOccurred())

	return epiFD
}

func (p *polProgramTest) runProgram(stateIn state.State, stateMap bpf.Map, progFD bpf.ProgFD, expProgRC int, expPolRC int) {
	// The policy program takes its input from the state map (rather than looking at the
	// packet).  Set up the state map.
	stateMapKey := []byte{0, 0, 0, 0} // State map has a single key
	stateBytesIn := stateIn.AsBytes()
	log.WithField("stateBytes", stateBytesIn).Debug("State bytes in")
	log.Debugf("State in %#v", stateIn)
	err := stateMap.Update(stateMapKey, stateBytesIn)
	Expect(err).NotTo(HaveOccurred(), "failed to update state map")

	log.Debug("Running BPF program")
	result, err := bpf.RunBPFProgram(progFD, make([]byte, 1000), 1)
	Expect(err).NotTo(HaveOccurred())

	log.Debug("Checking result...")
	stateBytesOut, err := stateMap.Get(stateMapKey)
	Expect(err).NotTo(HaveOccurred())
	log.WithField("stateBytes", stateBytesOut).Debug("State bytes out")
	stateOut := state.StateFromBytes(stateBytesOut)
	log.Debugf("State out %#v", stateOut)
	Expect(stateOut.PolicyRC).To(BeNumerically("==", expPolRC), "policy RC was incorrect")
	Expect(result.RC).To(BeNumerically("==", expProgRC), "program RC was incorrect")
	// Check no other fields got clobbered.
	expectedStateOut := stateIn
	expectedStateOut.PolicyRC = int32(expPolRC)
	Expect(stateOut).To(Equal(expectedStateOut), "policy program modified unexpected parts of the state")
}

func (p *polProgramTest) setUpIPSets(alloc *idalloc.IDAllocator, ipsMap bpf.Map) {
	for name, members := range p.IPSets {
		id := alloc.GetOrAlloc(name)
		for _, m := range members {
			entry := ipsets.ProtoIPSetMemberToBPFEntry(id, m)
			err := ipsMap.Update(entry[:], ipsets.DummyValue)
			Expect(err).NotTo(HaveOccurred())
		}
	}
}

func cleanIPSetMap() {
	// Clean out any existing IP sets.  (The other maps have a fixed number of keys that
	// we set as needed.)
	var keys [][]byte
	err := ipsMap.Iter(func(k, v []byte) bpf.IteratorAction {
		kCopy := make([]byte, len(k))
		copy(kCopy, k)
		keys = append(keys, kCopy)
		return bpf.IterNone
	})
	Expect(err).NotTo(HaveOccurred(), "failed to clean out map before test")
	for _, k := range keys {
		err = ipsMap.Delete(k)
		Expect(err).NotTo(HaveOccurred(), "failed to clean out map before test")
	}
}
