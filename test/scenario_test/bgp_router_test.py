# Copyright (C) 2015 Nippon Telegraph and Telephone Corporation.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
# implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import unittest
from fabric.api import local
from lib import base
from lib.gobgp import *
from lib.quagga import *
import sys
import os
import time
import nose
from noseplugin import OptionParser, parser_option
from itertools import chain


class GoBGPTestBase(unittest.TestCase):

    @classmethod
    def setUpClass(cls):
        gobgp_ctn_image_name = parser_option.gobgp_image
        base.TEST_PREFIX = parser_option.test_prefix

        g1 = GoBGPContainer(name='g1', asn=65000, router_id='192.168.0.1',
                            ctn_image_name=gobgp_ctn_image_name,
                            log_level=parser_option.gobgp_log_level)
        q1 = QuaggaBGPContainer(name='q1', asn=65001, router_id='192.168.0.2')
        q2 = QuaggaBGPContainer(name='q2', asn=65002, router_id='192.168.0.3')
        q3 = QuaggaBGPContainer(name='q3', asn=65003, router_id='192.168.0.4')

        qs = [q1, q2, q3]
        ctns = [g1, q1, q2, q3]

        # advertise a route from q1, q2, q3
        for idx, q in enumerate(qs):
            route = '10.0.{0}.0/24'.format(idx+1)
            q.add_route(route)

        initial_wait_time = max(ctn.run() for ctn in ctns)

        time.sleep(initial_wait_time)

        br01 = Bridge(name='br01', subnet='192.168.10.0/24')
        [br01.addif(ctn) for ctn in ctns]

        for q in qs:
            g1.add_peer(q)
            q.add_peer(g1)

        cls.gobgp = g1
        cls.quaggas = {'q1': q1, 'q2': q2, 'q3': q3}
        cls.bridges = {'br01': br01}

    # test each neighbor state is turned establish
    def test_01_neighbor_established(self):
        for q in self.quaggas.itervalues():
            self.gobgp.wait_for(expected_state=BGP_FSM_ESTABLISHED, peer=q)

    def test_02_check_gobgp_global_rib(self):
        for q in self.quaggas.itervalues():
            # paths expected to exist in gobgp's global rib
            routes = q.routes.keys()
            timeout = 120
            interval = 1
            count = 0
            while True:
                # gobgp's global rib
                global_rib = [p['prefix'] for p in self.gobgp.get_global_rib()]

                for p in global_rib:
                    if p in routes:
                        routes.remove(p)

                if len(routes) == 0:
                    break

                time.sleep(interval)
                count += interval
                if count >= timeout:
                    raise Exception('timeout')

    # check gobgp properly add it's own asn to aspath
    def test_03_check_gobgp_adj_out_rib(self):
        for q in self.quaggas.itervalues():
            for path in self.gobgp.get_adj_rib_out(q):
                asns = path['as_path']
                self.assertTrue(self.gobgp.asn in asns)

    # check routes are properly advertised to all BGP speaker
    def test_04_check_quagga_global_rib(self):
        interval = 1
        timeout = int(120/interval)
        for q in self.quaggas.itervalues():
            done = False
            for _ in range(timeout):
                if done:
                    break
                global_rib = q.get_global_rib()
                global_rib = [p['prefix'] for p in global_rib]
                if len(global_rib) < len(self.quaggas):
                    time.sleep(interval)
                    continue

                self.assertTrue(len(global_rib) == len(self.quaggas))

                for c in self.quaggas.itervalues():
                    for r in c.routes:
                        self.assertTrue(r in global_rib)
                done = True
            if done:
                continue
            # should not reach here
            self.assertTrue(False)

    def test_05_add_quagga(self):
        q4 = QuaggaBGPContainer(name='q4', asn=65004, router_id='192.168.0.5')
        self.quaggas['q4'] = q4

        q4.add_route('10.0.4.0/24')

        initial_wait_time = q4.run()
        time.sleep(initial_wait_time)
        self.bridges['br01'].addif(q4)
        self.gobgp.add_peer(q4)
        q4.add_peer(self.gobgp)

        self.gobgp.wait_for(expected_state=BGP_FSM_ESTABLISHED, peer=q4)

    def test_06_check_global_rib(self):
        self.test_02_check_gobgp_global_rib()
        self.test_04_check_quagga_global_rib()

    def test_07_stop_one_quagga(self):
        q4 = self.quaggas['q4']
        q4.stop()
        self.gobgp.wait_for(expected_state=BGP_FSM_ACTIVE, peer=q4)
        del self.quaggas['q4']

    # check gobgp properly send withdrawal message with q4's route
    def test_08_check_global_rib(self):
        self.test_02_check_gobgp_global_rib()
        self.test_04_check_quagga_global_rib()

    def test_09_add_distant_relative(self):
        q1 = self.quaggas['q1']
        q2 = self.quaggas['q2']
        q3 = self.quaggas['q3']
        q5 = QuaggaBGPContainer(name='q5', asn=65005, router_id='192.168.0.6')

        initial_wait_time = q5.run()
        time.sleep(initial_wait_time)

        br02 = Bridge(name='br02', subnet='192.168.20.0/24')
        br02.addif(q5)
        br02.addif(q2)

        br03 = Bridge(name='br03', subnet='192.168.30.0/24')
        br03.addif(q5)
        br03.addif(q3)

        for q in [q2, q3]:
            q5.add_peer(q)
            q.add_peer(q5)

        med200 = {'name': 'med200',
                  'type': 'permit',
                  'match': '0.0.0.0/0',
                  'direction': 'out',
                  'med': 200}
        q2.add_policy(med200, self.gobgp)
        med100 = {'name': 'med100',
                  'type': 'permit',
                  'match': '0.0.0.0/0',
                  'direction': 'out',
                  'med': 100}
        q3.add_policy(med100, self.gobgp)

        q5.add_route('10.0.6.0/24')

        self.gobgp.wait_for(expected_state=BGP_FSM_ESTABLISHED, peer=q2)
        self.gobgp.wait_for(expected_state=BGP_FSM_ESTABLISHED, peer=q3)
        q2.wait_for(expected_state=BGP_FSM_ESTABLISHED, peer=q5)
        q3.wait_for(expected_state=BGP_FSM_ESTABLISHED, peer=q5)

        timeout = 120
        interval = 1
        count = 0
        while True:
            paths = self.gobgp.get_adj_rib_out(q1, '10.0.6.0/24')
            if len(paths) > 0:
                path = paths[0]
                print "{0}'s nexthop is {1}".format(path['nlri']['prefix'],
                                                    path['nexthop'])
                n_addrs = [i[1].split('/')[0] for i in self.gobgp.ip_addrs]
                if path['nexthop'] in n_addrs:
                    break

            time.sleep(interval)
            count += interval
            if count >= timeout:
                raise Exception('timeout')

    def test_10_originate_path(self):
        self.gobgp.add_route('10.10.0.0/24')
        dst = self.gobgp.get_global_rib('10.10.0.0/24')
        self.assertTrue(len(dst) == 1)
        self.assertTrue(len(dst[0]['paths']) == 1)
        path = dst[0]['paths'][0]
        self.assertTrue(path['nexthop'] == '0.0.0.0')
        self.assertTrue(len(path['as_path']) == 0)

    def test_11_check_adj_rib_out(self):
        for q in self.quaggas.itervalues():
            paths = self.gobgp.get_adj_rib_out(q, '10.10.0.0/24')
            self.assertTrue(len(paths) == 1)
            path = paths[0]
            peer_info = self.gobgp.peers[q]
            local_addr = peer_info['local_addr'].split('/')[0]
            self.assertTrue(path['nexthop'] == local_addr)
            self.assertTrue(path['as_path'] == [self.gobgp.asn])

    def test_12_disable_peer(self):
        q1 = self.quaggas['q1']
        self.gobgp.disable_peer(q1)
        self.gobgp.wait_for(expected_state=BGP_FSM_IDLE, peer=q1)

        time.sleep(3)

        for route in q1.routes.iterkeys():
            dst = self.gobgp.get_global_rib(route)
            self.assertTrue(len(dst) == 0)

            for q in self.quaggas.itervalues():
                if q is q1:
                    continue
                paths = self.gobgp.get_adj_rib_out(q, route)
                self.assertTrue(len(paths) == 0)

    def test_13_enable_peer(self):
        q1 = self.quaggas['q1']
        self.gobgp.enable_peer(q1)
        self.gobgp.wait_for(expected_state=BGP_FSM_ESTABLISHED, peer=q1)

    def test_14_check_adj_rib_out(self):
        self.test_11_check_adj_rib_out()

    def test_15_check_active_connection(self):
        g1 = self.gobgp
        g2 = GoBGPContainer(name='g2', asn=65000, router_id='192.168.0.5',
                            ctn_image_name=self.gobgp.image,
                            log_level=parser_option.gobgp_log_level)
        g2.run()
        br01 = self.bridges['br01']
        br01.addif(g2)
        g2.add_peer(g1, passive=True)
        g1.add_peer(g2)
        g1.wait_for(expected_state=BGP_FSM_ESTABLISHED, peer=g2)


if __name__ == '__main__':
    if os.geteuid() is not 0:
        print "you are not root."
        sys.exit(1)
    output = local("which docker 2>&1 > /dev/null ; echo $?", capture=True)
    if int(output) is not 0:
        print "docker not found"
        sys.exit(1)

    nose.main(argv=sys.argv, addplugins=[OptionParser()],
              defaultTest=sys.argv[0])
