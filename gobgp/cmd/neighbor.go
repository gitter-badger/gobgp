// Copyright (C) 2015 Nippon Telegraph and Telephone Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/osrg/gobgp/api"
	"github.com/osrg/gobgp/config"
	"github.com/osrg/gobgp/packet"
	"github.com/osrg/gobgp/policy"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
	"io"
	"net"
	"os"
	"sort"
	"strings"
)

func getNeighbors() (peers, error) {
	arg := &api.Arguments{}
	stream, e := client.GetNeighbors(context.Background(), arg)
	if e != nil {
		fmt.Println(e)
		return nil, e
	}
	m := peers{}
	for {
		p, e := stream.Recv()
		if e == io.EOF {
			break
		} else if e != nil {
			return nil, e
		}
		if neighborsOpts.Transport != "" {
			addr := net.ParseIP(p.Conf.RemoteIp)
			if addr.To4() != nil {
				if neighborsOpts.Transport != "ipv4" {
					continue
				}
			} else {
				if neighborsOpts.Transport != "ipv6" {
					continue
				}
			}
		}
		m = append(m, p)
	}
	return m, nil
}

func showNeighbors() error {
	m, err := getNeighbors()
	if err != nil {
		return err
	}
	if globalOpts.Json {
		j, _ := json.Marshal(m)
		fmt.Println(string(j))
		return nil
	}

	if globalOpts.Quiet {
		for _, p := range m {
			fmt.Println(p.Conf.RemoteIp)
		}
		return nil
	}
	maxaddrlen := 0
	maxaslen := 0
	maxtimelen := len("Up/Down")
	timedelta := []string{}

	sort.Sort(m)

	for _, p := range m {
		if len(p.Conf.RemoteIp) > maxaddrlen {
			maxaddrlen = len(p.Conf.RemoteIp)
		}

		if len(fmt.Sprint(p.Conf.RemoteAs)) > maxaslen {
			maxaslen = len(fmt.Sprint(p.Conf.RemoteAs))
		}
		var t string
		if p.Info.Uptime == 0 {
			t = "never"
		} else if p.Info.BgpState == "BGP_FSM_ESTABLISHED" {
			t = formatTimedelta(p.Info.Uptime)
		} else {
			t = formatTimedelta(p.Info.Downtime)
		}
		if len(t) > maxtimelen {
			maxtimelen = len(t)
		}
		timedelta = append(timedelta, t)
	}
	var format string
	format = "%-" + fmt.Sprint(maxaddrlen) + "s" + " %" + fmt.Sprint(maxaslen) + "s" + " %" + fmt.Sprint(maxtimelen) + "s"
	format += " %-11s |%11s %8s %8s\n"
	fmt.Printf(format, "Peer", "AS", "Up/Down", "State", "#Advertised", "Received", "Accepted")
	format_fsm := func(admin, fsm string) string {
		if admin == "ADMIN_STATE_DOWN" {
			return "Idle(Admin)"
		}

		if fsm == "BGP_FSM_IDLE" {
			return "Idle"
		} else if fsm == "BGP_FSM_CONNECT" {
			return "Connect"
		} else if fsm == "BGP_FSM_ACTIVE" {
			return "Active"
		} else if fsm == "BGP_FSM_OPENSENT" {
			return "Sent"
		} else if fsm == "BGP_FSM_OPENCONFIRM" {
			return "Confirm"
		} else {
			return "Establ"
		}
	}

	for i, p := range m {
		fmt.Printf(format, p.Conf.RemoteIp, fmt.Sprint(p.Conf.RemoteAs), timedelta[i], format_fsm(p.Info.AdminState, p.Info.BgpState), fmt.Sprint(p.Info.Advertized), fmt.Sprint(p.Info.Received), fmt.Sprint(p.Info.Accepted))
	}

	return nil
}

func showNeighbor(args []string) error {
	id := &api.Arguments{
		Name: args[0],
	}
	p, e := client.GetNeighbor(context.Background(), id)
	if e != nil {
		return e
	}

	if globalOpts.Json {
		j, _ := json.Marshal(p)
		fmt.Println(string(j))
		return nil
	}

	fmt.Printf("BGP neighbor is %s, remote AS %d\n", p.Conf.RemoteIp, p.Conf.RemoteAs)
	fmt.Printf("  BGP version 4, remote router ID %s\n", p.Conf.Id)
	fmt.Printf("  BGP state = %s, up for %s\n", p.Info.BgpState, formatTimedelta(p.Info.Uptime))
	fmt.Printf("  BGP OutQ = %d, Flops = %d\n", p.Info.OutQ, p.Info.Flops)
	fmt.Printf("  Hold time is %d, keepalive interval is %d seconds\n", p.Info.NegotiatedHoldtime, p.Info.KeepaliveInterval)
	fmt.Printf("  Configured hold time is %d, keepalive interval is %d seconds\n", p.Conf.Holdtime, p.Conf.KeepaliveInterval)

	fmt.Printf("  Neighbor capabilities:\n")
	caps := capabilities{}
	lookup := func(val *api.Capability, l capabilities) *api.Capability {
		for _, v := range l {
			if v.Code == val.Code {
				if v.Code == api.BGP_CAPABILITY_MULTIPROTOCOL {
					if v.MultiProtocol == val.MultiProtocol {
						return v
					}
					continue
				}
				return v
			}
		}
		return nil
	}
	caps = append(caps, p.Conf.LocalCap...)
	for _, v := range p.Conf.RemoteCap {
		if lookup(v, caps) == nil {
			caps = append(caps, v)
		}
	}

	sort.Sort(caps)

	firstMp := true

	for _, c := range caps {
		support := ""
		if m := lookup(c, p.Conf.LocalCap); m != nil {
			support += "advertised"
		}
		if lookup(c, p.Conf.RemoteCap) != nil {
			if len(support) != 0 {
				support += " and "
			}
			support += "received"
		}

		if c.Code != api.BGP_CAPABILITY_MULTIPROTOCOL {
			fmt.Printf("    %s:\t%s\n", c.Code, support)
		} else {
			if firstMp {
				fmt.Printf("    %s:\n", c.Code)
				firstMp = false
			}
			fmt.Printf("        %s:\t%s\n", bgp.RouteFamily(c.MultiProtocol), support)

		}
	}
	fmt.Print("  Message statistics:\n")
	fmt.Print("                         Sent       Rcvd\n")
	fmt.Printf("    Opens:         %10d %10d\n", p.Info.OpenMessageOut, p.Info.OpenMessageIn)
	fmt.Printf("    Notifications: %10d %10d\n", p.Info.NotificationOut, p.Info.NotificationIn)
	fmt.Printf("    Updates:       %10d %10d\n", p.Info.UpdateMessageOut, p.Info.UpdateMessageIn)
	fmt.Printf("    Keepalives:    %10d %10d\n", p.Info.KeepAliveMessageOut, p.Info.KeepAliveMessageIn)
	fmt.Printf("    Route Refesh:  %10d %10d\n", p.Info.RefreshMessageOut, p.Info.RefreshMessageIn)
	fmt.Printf("    Discarded:     %10d %10d\n", p.Info.DiscardedOut, p.Info.DiscardedIn)
	fmt.Printf("    Total:         %10d %10d\n", p.Info.TotalMessageOut, p.Info.TotalMessageIn)

	return nil
}

type AsPathFormat struct {
	start     string
	end       string
	separator string
}

func showRoute(pathList []*Path, showAge bool, showBest bool, isMonitor, printHeader bool) {

	var pathStrs [][]interface{}
	maxPrefixLen := 20
	maxNexthopLen := 20
	maxAsPathLen := 20
	aspath := func(a bgp.PathAttributeInterface) string {

		delimiter := make(map[uint8]*AsPathFormat)
		delimiter[bgp.BGP_ASPATH_ATTR_TYPE_SET] = &AsPathFormat{"{", "}", ","}
		delimiter[bgp.BGP_ASPATH_ATTR_TYPE_SEQ] = &AsPathFormat{"", "", " "}
		delimiter[bgp.BGP_ASPATH_ATTR_TYPE_CONFED_SEQ] = &AsPathFormat{"(", ")", " "}
		delimiter[bgp.BGP_ASPATH_ATTR_TYPE_CONFED_SET] = &AsPathFormat{"[", "]", ","}

		var segments []string = make([]string, 0)
		aspaths := a.(*bgp.PathAttributeAsPath).Value
		for _, aspath := range aspaths {
			var t uint8
			var asnsStr []string
			switch aspath.(type) {
			case *bgp.AsPathParam:
				a := aspath.(*bgp.AsPathParam)
				t = a.Type
				for _, asn := range a.AS {
					asnsStr = append(asnsStr, fmt.Sprintf("%d", asn))
				}
			case *bgp.As4PathParam:
				a := aspath.(*bgp.As4PathParam)
				t = a.Type
				for _, asn := range a.AS {
					asnsStr = append(asnsStr, fmt.Sprintf("%d", asn))
				}
			}
			s := bytes.NewBuffer(make([]byte, 0, 64))
			start := delimiter[t].start
			end := delimiter[t].end
			separator := delimiter[t].separator
			s.WriteString(start)
			s.WriteString(strings.Join(asnsStr, separator))
			s.WriteString(end)
			segments = append(segments, s.String())
		}
		return strings.Join(segments, " ")
	}

	for _, p := range pathList {
		var nexthop string
		var aspathstr string

		s := []string{}
		for _, a := range p.PathAttrs {
			switch a.GetType() {
			case bgp.BGP_ATTR_TYPE_NEXT_HOP:
				nexthop = a.(*bgp.PathAttributeNextHop).Value.String()
			case bgp.BGP_ATTR_TYPE_MP_REACH_NLRI:
				n := a.(*bgp.PathAttributeMpReachNLRI).Nexthop
				if n != nil {
					nexthop = n.String()
				} else {
					nexthop = "fictitious"
				}
			case bgp.BGP_ATTR_TYPE_AS_PATH:
				aspathstr = aspath(a)
			case bgp.BGP_ATTR_TYPE_AS4_PATH:
				continue
			default:
				s = append(s, a.String())
			}
		}
		pattrstr := fmt.Sprint(s)

		if maxNexthopLen < len(nexthop) {
			maxNexthopLen = len(nexthop)
		}

		if maxAsPathLen < len(aspathstr) {
			maxAsPathLen = len(aspathstr)
		}

		best := ""
		switch config.RpkiValidationResultType(p.Validation) {
		case config.RPKI_VALIDATION_RESULT_TYPE_NOT_FOUND:
			best += "N"
		case config.RPKI_VALIDATION_RESULT_TYPE_VALID:
			best += "V"
		case config.RPKI_VALIDATION_RESULT_TYPE_INVALID:
			best += "I"
		}
		if showBest {
			if p.Best {
				best += "*>"
			} else {
				best += "* "
			}
		}

		if maxPrefixLen < len(p.Nlri.String()) {
			maxPrefixLen = len(p.Nlri.String())
		}

		if isMonitor {
			title := "ROUTE"
			if p.IsWithdraw {
				title = "DELROUTE"
			}
			pathStrs = append(pathStrs, []interface{}{title, p.Nlri.String(), nexthop, aspathstr, pattrstr})
		} else if showAge {
			pathStrs = append(pathStrs, []interface{}{best, p.Nlri.String(), nexthop, aspathstr, formatTimedelta(p.Age), pattrstr})
		} else {
			pathStrs = append(pathStrs, []interface{}{best, p.Nlri.String(), nexthop, aspathstr, pattrstr})
		}
	}

	var format string
	if isMonitor {
		format = "[%s] %s via %s aspath [%s] attrs %s\n"
	} else if showAge {
		format = fmt.Sprintf("%%-3s %%-%ds %%-%ds %%-%ds %%-10s %%-s\n", maxPrefixLen, maxNexthopLen, maxAsPathLen)
		if printHeader {
			fmt.Printf(format, "", "Network", "Next Hop", "AS_PATH", "Age", "Attrs")
		}
	} else {
		format = fmt.Sprintf("%%-3s %%-%ds %%-%ds %%-%ds %%-s\n", maxPrefixLen, maxNexthopLen, maxAsPathLen)
		if printHeader {
			fmt.Printf(format, "", "Network", "Next Hop", "AS_PATH", "Attrs")
		}
	}

	for _, pathStr := range pathStrs {
		fmt.Printf(format, pathStr...)
	}
}

func showNeighborRib(r string, name string, args []string) error {
	var resource api.Resource
	showBest := false
	showAge := true
	switch r {
	case CMD_GLOBAL:
		showBest = true
		resource = api.Resource_GLOBAL
	case CMD_LOCAL:
		showBest = true
		resource = api.Resource_LOCAL
	case CMD_ADJ_IN:
		resource = api.Resource_ADJ_IN
	case CMD_ADJ_OUT:
		showAge = false
		resource = api.Resource_ADJ_OUT
	case CMD_VRF:
		resource = api.Resource_VRF
	}
	rf, err := checkAddressFamily(net.ParseIP(name))
	if err != nil {
		return err
	}

	var prefix string
	var host net.IP
	if len(args) > 0 {
		if rf != bgp.RF_IPv4_UC && rf != bgp.RF_IPv6_UC {
			return fmt.Errorf("route filtering is only supported for IPv4/IPv6 unicast routes")
		}
		_, p, err := net.ParseCIDR(args[0])
		if err != nil {
			host = net.ParseIP(args[0])
			if host == nil {
				return err
			}
		} else {
			prefix = p.String()
		}
	}

	arg := &api.Arguments{
		Resource: resource,
		Rf:       uint32(rf),
		Name:     name,
	}

	stream, err := client.GetRib(context.Background(), arg)
	if err != nil {
		return err
	}

	isResultSorted := func(rf bgp.RouteFamily) bool {
		switch rf {
		case bgp.RF_IPv4_UC, bgp.RF_IPv6_UC:
			return true
		}
		return false
	}

	dsts := []*Destination{}
	maxOnes := 0
	counter := 0
	for {
		d, e := stream.Recv()
		if e == io.EOF {
			break
		} else if e != nil {
			return e
		}
		if prefix != "" && prefix != d.Prefix {
			continue
		}
		if host != nil {
			_, prefix, _ := net.ParseCIDR(d.Prefix)
			ones, _ := prefix.Mask.Size()
			if prefix.Contains(host) {
				if maxOnes < ones {
					dsts = []*Destination{}
					maxOnes = ones
				} else if maxOnes > ones {
					continue
				}
			} else {
				continue
			}
		}

		dst, err := ApiStruct2Destination(d)
		if err != nil {
			return err
		}
		if isResultSorted(rf) && !globalOpts.Json && len(dst.Paths) > 0 {
			ps := paths{}
			ps = append(ps, dst.Paths...)
			sort.Sort(ps)
			if counter == 0 {
				showRoute(ps, showAge, showBest, false, true)
			} else {
				showRoute(ps, showAge, showBest, false, false)
			}
			counter++
		}
		dsts = append(dsts, dst)
	}

	if globalOpts.Json {
		j, _ := json.Marshal(dsts)
		fmt.Println(string(j))
		return nil
	}

	if isResultSorted(rf) && counter != 0 {
		// we already showed
		return nil
	}

	ps := paths{}
	for _, dst := range dsts {
		ps = append(ps, dst.Paths...)
	}

	if len(ps) == 0 {
		fmt.Println("Network not in table")
		return nil
	}

	sort.Sort(ps)
	showRoute(ps, showAge, showBest, false, true)
	return nil
}

func resetNeighbor(cmd string, remoteIP string, args []string) error {
	rf, err := checkAddressFamily(net.ParseIP(remoteIP))
	if err != nil {
		return err
	}
	arg := &api.Arguments{
		Name: remoteIP,
		Rf:   uint32(rf),
	}
	switch cmd {
	case CMD_RESET:
		client.Reset(context.Background(), arg)
	case CMD_SOFT_RESET:
		client.SoftReset(context.Background(), arg)
	case CMD_SOFT_RESET_IN:
		client.SoftResetIn(context.Background(), arg)
	case CMD_SOFT_RESET_OUT:
		client.SoftResetOut(context.Background(), arg)
	}
	return nil
}

func stateChangeNeighbor(cmd string, remoteIP string, args []string) error {
	arg := &api.Arguments{
		Rf:   uint32(bgp.RF_IPv4_UC),
		Name: remoteIP,
	}
	var err error
	switch cmd {
	case CMD_SHUTDOWN:
		_, err = client.Shutdown(context.Background(), arg)
	case CMD_ENABLE:
		_, err = client.Enable(context.Background(), arg)
	case CMD_DISABLE:
		_, err = client.Disable(context.Background(), arg)
	}
	return err
}

func showNeighborPolicy(remoteIP net.IP) error {
	rf, err := checkAddressFamily(net.IP{})
	if err != nil {
		return err
	}
	arg := &api.Arguments{
		Rf:   uint32(rf),
		Name: remoteIP.String(),
	}

	ap, e := client.GetNeighborPolicy(context.Background(), arg)
	if e != nil {
		return e
	}

	if globalOpts.Json {
		j, _ := json.Marshal(ap)
		fmt.Println(string(j))
		return nil
	}

	fmt.Printf("DefaultImportPolicy: %s\n", ap.DefaultImportPolicy)
	fmt.Printf("DefaultExportPolicy: %s\n", ap.DefaultExportPolicy)
	fmt.Printf("DefaultInPolicy: %s\n", ap.DefaultInPolicy)
	fmt.Printf("ImportPolicies:\n")
	for _, inPolicy := range ap.ImportPolicies {
		fmt.Printf("  PolicyName %s:\n", inPolicy.PolicyDefinitionName)
		showPolicyStatement(2, inPolicy)
	}
	fmt.Printf("ExportPolicies:\n")
	for _, outPolicy := range ap.ExportPolicies {
		fmt.Printf("  PolicyName %s:\n", outPolicy.PolicyDefinitionName)
		showPolicyStatement(2, outPolicy)
	}
	fmt.Printf("InPolicies:\n")
	for _, distPolicy := range ap.InPolicies {
		fmt.Printf("  PolicyName %s:\n", distPolicy.PolicyDefinitionName)
		showPolicyStatement(2, distPolicy)
	}
	return nil
}

func parsePolicy(pNames string) []*api.PolicyDefinition {
	pList := strings.Split(pNames, ",")
	policyList := make([]*api.PolicyDefinition, 0, len(pList))
	for _, p := range pList {
		if p != "" {
			policy := &api.PolicyDefinition{
				PolicyDefinitionName: p,
			}
			policyList = append(policyList, policy)
		}
	}
	return policyList
}

func modNeighborPolicy(remoteIP net.IP, cmdType string, eArg []string) error {
	var operation api.Operation
	pol := &api.ApplyPolicy{}
	switch cmdType {
	case CMD_ADD:
		if len(eArg) < 4 {
			return fmt.Errorf("Usage: gobgp neighbor <ipaddr> policy %s {%s|%s|%s} <policies> {%s|%s}", cmdType, CMD_IMPORT, CMD_EXPORT, CMD_IN, policy.ROUTE_ACCEPT, policy.ROUTE_REJECT)
		}
		policies := parsePolicy(eArg[1])
		defaultPolicy, err := parseRouteAction(eArg[2])
		if err != nil {
			return err
		}
		switch eArg[0] {
		case CMD_IMPORT:
			pol.ImportPolicies = policies
			pol.DefaultImportPolicy = defaultPolicy
		case CMD_EXPORT:
			pol.ExportPolicies = policies
			pol.DefaultExportPolicy = defaultPolicy
		case CMD_IN:
			pol.InPolicies = policies
			pol.DefaultInPolicy = defaultPolicy
		}
		operation = api.Operation_ADD

	case CMD_DEL:
		operation = api.Operation_DEL
	}
	arg := &api.PolicyArguments{
		Resource:        api.Resource_POLICY_ROUTEPOLICY,
		Operation:       operation,
		NeighborAddress: remoteIP.String(),
		Name:            eArg[0],
		ApplyPolicy:     pol,
	}
	stream, err := client.ModNeighborPolicy(context.Background())
	if err != nil {
		return err
	}
	err = stream.Send(arg)
	if err != nil {
		return err
	}
	stream.CloseSend()

	res, e := stream.Recv()
	if e != nil {
		return e
	}
	if res.Code != api.Error_SUCCESS {
		return fmt.Errorf("error: code: %d, msg: %s", res.Code, res.Msg)
	}
	return nil
}

func NewNeighborCmd() *cobra.Command {

	neighborCmdImpl := &cobra.Command{}

	type cmds struct {
		names []string
		f     func(string, string, []string) error
	}

	c := make([]cmds, 0, 3)
	c = append(c, cmds{[]string{CMD_LOCAL, CMD_ADJ_IN, CMD_ADJ_OUT}, showNeighborRib})
	c = append(c, cmds{[]string{CMD_RESET, CMD_SOFT_RESET, CMD_SOFT_RESET_IN, CMD_SOFT_RESET_OUT}, resetNeighbor})
	c = append(c, cmds{[]string{CMD_SHUTDOWN, CMD_ENABLE, CMD_DISABLE}, stateChangeNeighbor})

	for _, v := range c {
		f := v.f
		for _, name := range v.names {
			c := &cobra.Command{
				Use: name,
				Run: func(cmd *cobra.Command, args []string) {
					remoteIP := net.ParseIP(args[len(args)-1])
					if remoteIP == nil {
						fmt.Println("invalid ip address:", args[len(args)-1])
						os.Exit(1)
					}
					err := f(cmd.Use, remoteIP.String(), args[:len(args)-1])
					if err != nil {
						fmt.Println(err)
						os.Exit(1)
					}
				},
			}
			neighborCmdImpl.AddCommand(c)
		}
	}

	policyCmd := &cobra.Command{
		Use: CMD_POLICY,
		Run: func(cmd *cobra.Command, args []string) {
			var err error
			remoteIP := net.ParseIP(args[0])
			if remoteIP == nil {
				err = fmt.Errorf("invalid ip address: %s", args[0])
			} else {
				err = showNeighborPolicy(remoteIP)
			}
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		},
	}

	for _, v := range []string{CMD_ADD, CMD_DEL} {
		cmd := &cobra.Command{
			Use: v,
			Run: func(cmd *cobra.Command, args []string) {
				var err error
				remoteIP := net.ParseIP(args[len(args)-1])
				if remoteIP == nil {
					fmt.Println("invalid ip address:", args[len(args)-1])
					os.Exit(1)
				}
				err = modNeighborPolicy(remoteIP, cmd.Use, args)
				if err != nil {
					fmt.Println(err)
					os.Exit(1)
				}
			},
		}
		policyCmd.AddCommand(cmd)
	}

	neighborCmdImpl.AddCommand(policyCmd)

	neighborCmd := &cobra.Command{
		Use: CMD_NEIGHBOR,
		Run: func(cmd *cobra.Command, args []string) {
			var err error
			if len(args) == 0 {
				err = showNeighbors()
			} else if len(args) == 1 {
				remoteIP := net.ParseIP(args[0])
				if remoteIP == nil {
					err = fmt.Errorf("invalid ip address: %s", args[0])
				} else {
					err = showNeighbor(args)
				}
			} else {
				args = append(args[1:], args[0])
				neighborCmdImpl.SetArgs(args)
				err = neighborCmdImpl.Execute()
			}
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		},
	}
	neighborCmd.PersistentFlags().StringVarP(&subOpts.AddressFamily, "address-family", "a", "", "address family")
	return neighborCmd
}
