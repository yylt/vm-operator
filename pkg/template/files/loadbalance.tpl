heat_template_version: 2016-10-14
resources:
  ######################################################################
  #
  # load balance
  #

  lb:
    type: 'OS::Neutron::LBaaS::LoadBalancer'
    properties:
      name: {{ $.loadbalance.name }}
      vip_subnet: {{ .loadbalance.subnet.subnet_id }}
{{ if .loadbalance.loadbalance_ip }}
      vip_address: {{ .loadbalance.loadbalance_ip }}
{{ end }}

{{ range $index, $v := $.loadbalance.port_map }}

  {{ $.loadbalance.name }}-pool{{ $index }}:
    type: 'OS::Neutron::LBaaS::Pool'
    depends_on: listener{{ $index }}
    properties:
      lb_algorithm: ROUND_ROBIN
      protocol: {{ $v.protocol }}
      listener: {get_resource: listener{{ $index }}}

  {{ $.loadbalance.name }}-listen{{ $index }}:
    type: 'OS::Neutron::LBaaS::Listener'
    depends_on: lb
    properties:
      loadbalancer: {get_resource: lb}
      protocol: {{ $v.protocol }}
      protocol_port: {{ $v.port }}
      connection_limit: -1

{{ range $ipindex, $ip := $v.ips }}
  {{ $.loadbalance.name }}-member{{ $ipindex }}:
    type: 'OS::Neutron::LBaaS::PoolMember'
    depends_on: lbpool{{ $index }}
    properties:
      pool:  {get_resource: lbpool{{ $index }} }
      subnet: {{ $.loadbalance.subnet.subnet_id }}
      protocol_port: {{ $v.port }}
      weight: 1
      address: {{ $ip }}
{{ end }}
{{ end }}