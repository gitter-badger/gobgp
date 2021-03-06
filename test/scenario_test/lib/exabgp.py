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

from base import *


class ExaBGPContainer(BGPContainer):

    SHARED_VOLUME = '/root/shared_volume'

    def __init__(self, name, asn, router_id, ctn_image_name='osrg/exabgp',
                 exabgp_path=''):
        super(ExaBGPContainer, self).__init__(name, asn, router_id,
                                              ctn_image_name)
        self.shared_volumes.append((self.config_dir, self.SHARED_VOLUME))
        self.exabgp_path = exabgp_path

    def _start_exabgp(self):
        cmd = CmdBuffer(' ')
        cmd << 'env exabgp.log.destination={0}/exabgpd.log'.format(self.SHARED_VOLUME)
        cmd << './exabgp/sbin/exabgp {0}/exabgpd.conf'.format(self.SHARED_VOLUME)
        self.local(str(cmd), flag='-d')

    def _update_exabgp(self):
        if self.exabgp_path == '':
            return
        c = CmdBuffer()
        c << '#!/bin/bash'

        remotepath = '/root/exabgp'
        localpath = self.exabgp_path
        local('cp -r {0} {1}'.format(localpath, self.config_dir))
        c << 'cp {0}/etc/exabgp/exabgp.env {1}'.format(remotepath, self.SHARED_VOLUME)
        c << 'sed -i -e \'s/all = false/all = true/g\' {0}/exabgp.env'.format(self.SHARED_VOLUME)
        c << 'cp -r {0}/exabgp {1}'.format(self.SHARED_VOLUME,
                                           remotepath[:-1*len('exabgp')])
        c << 'cp {0}/exabgp.env {1}/etc/exabgp/'.format(self.SHARED_VOLUME, remotepath)
        cmd = 'echo "{0:s}" > {1}/update.sh'.format(c, self.config_dir)
        local(cmd, capture=True)
        cmd = 'chmod 755 {0}/update.sh'.format(self.config_dir)
        local(cmd, capture=True)
        cmd = '{0}/update.sh'.format(self.SHARED_VOLUME)
        self.local(cmd)

    def run(self):
        super(ExaBGPContainer, self).run()
        self._update_exabgp()
        self._start_exabgp()
        return self.WAIT_FOR_BOOT

    def create_config(self):
        cmd = CmdBuffer()
        for peer, info in self.peers.iteritems():
            cmd << 'neighbor {0} {{'.format(info['neigh_addr'].split('/')[0])
            cmd << '    router-id {0};'.format(self.router_id)

            local_addr = ''
            for me, you in itertools.product(self.ip_addrs, peer.ip_addrs):
                if me[2] == you[2]:
                    local_addr = me[1]
            if local_addr == '':
                raise Exception('local_addr not found')
            local_addr = local_addr.split('/')[0]
            cmd << '    local-address {0};'.format(local_addr)
            cmd << '    local-as {0};'.format(self.asn)
            cmd << '    peer-as {0};'.format(peer.asn)

            routes = [r for r in self.routes.values() if r['rf'] == 'ipv4' or r['rf'] == 'ipv6']

            if len(routes) > 0:
                cmd << '    static {'
                for route in routes:
                    r = CmdBuffer(' ')
                    nexthop = local_addr
                    if route['next-hop']:
                        nexthop = route['next-hop']
                    r << '      route {0} next-hop {1}'.format(route['prefix'], nexthop)
                    if route['as-path']:
                        r << 'as-path [{0}]'.format(' '.join(str(i) for i in route['as-path']))
                    if route['community']:
                        r << 'community [{0}]'.format(' '.join(c for c in route['community']))
                    if route['med']:
                        r << 'med {0}'.format(route['med'])
                    if route['extended-community']:
                        r << 'extended-community [{0}]'.format(route['extended-community'])
                    if route['attr']:
                        r << 'attribute [ {0} ]'.format(route['attr'])

                    cmd << '{0};'.format(str(r))
                cmd << '    }'

            routes = [r for r in self.routes.values() if r['rf'] == 'ipv4-flowspec']

            if len(routes) > 0:
                cmd << '    flow {'
                for route in routes:
                    cmd << '        route {0}{{'.format(route['prefix'])
                    cmd << '            match {'
                    for match in route['matchs']:
                        cmd << '                {0};'.format(match)
#                    cmd << '                source {0};'.format(route['prefix'])
#                    cmd << '                destination 192.168.0.1/32;'
#                    cmd << '                destination-port =3128 >8080&<8088;'
#                    cmd << '                source-port >1024;'
#                    cmd << '                port =14 =15 >10&<156;'
#                    cmd << '                protocol udp;' # how to specify multiple ip protocols
#                    cmd << '                packet-length >1000&<2000;'
#                    cmd << '                tcp-flags !syn;'
                    cmd << '            }'
                    cmd << '            then {'
                    for then in route['thens']:
                        cmd << '                {0};'.format(then)
#                    cmd << '                accept;'
#                    cmd << '                discard;'
#                    cmd << '                rate-limit 9600;'
#                    cmd << '                redirect 1.2.3.4:100;'
#                    cmd << '                redirect 100:100;'
#                    cmd << '                mark 10;'
#                    cmd << '                action sample-terminal;'
                    cmd << '            }'
                    cmd << '        }'
                cmd << '    }'
            cmd << '}'

        with open('{0}/exabgpd.conf'.format(self.config_dir), 'w') as f:
            print colors.yellow('[{0}\'s new config]'.format(self.name))
            print colors.yellow(str(cmd))
            f.write(str(cmd))

    def reload_config(self):
        if len(self.peers) == 0:
            return

        def _reload():
            def _is_running():
                ps = self.local('ps', capture=True)
                running = False
                for line in ps.split('\n')[1:]:
                    if 'python' in line:
                        running = True
                return running
            if _is_running():
                self.local('/usr/bin/pkill python -SIGUSR1')
            else:
                self._start_exabgp()
            time.sleep(1)
            if not _is_running():
                raise RuntimeError()
        try_several_times(_reload)
