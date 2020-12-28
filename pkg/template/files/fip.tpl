heat_template_version: 2016-10-14
resources:
  ######################################################################
  #
  # floating ip
  #
{{ if $.publicip.address.ip }}
  {{ .publicip.name }}:
    type: 'OS::Neutron::FloatingIPAssociation'
    properties:
      floatingip_id: {{ .publicip.float_id }}
      fixed_ip_address: {{ .publicip.fixed_ip }}
      port_id: {{ .publicip.port_id }}

{{ else }}
  {{ .publicip.name }}_fipqos:
    type: 'OS::Neutron::QoSPolicy'
  {{ .publicip.name }}_qosbandwidthrule:
    type: 'OS::Neutron::QoSBandwidthLimitRule'
    properties:
      max_kbps: {{ .publicip.Mbps }}
      policy:
        get_resource: {{ .publicip.name }}_fipqos
  {{ .publicip.name }}:
    type: 'OS::Neutron::FloatingIP'
    properties:
      floating_network: {{ .publicip.subnet.network_id }}
      fixed_ip_address: {{ .publicip.fixed_ip }}
      port_id: {{ .publicip.port_id }}
      qos_policy:
        get_resource: {{ .publicip.name }}_fipqos

{{ end }}