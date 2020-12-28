heat_template_version: 2016-10-14
resources:
  ######################################################################
  #
  # vitual machines
  #

{{ range $intindex := intRange .server.replicas }}
{{ if $.server.boot_volume_id }}
{{ else }}
  boot_volume{{ $intindex }}:
    type: OS::Cinder::Volume
    properties:
      size: {{ $.server.boot_volume.volume_size }}
      volume_type: {{ $.server.boot_volume.volume_type }}
      image: {{ $.server.boot_image }}
      availability_zone: {{ $.server.availability_zone }}
{{ end }}

{{ range $index, $v := $.server.volumes }}
  node{{ $intindex }}data_volume{{ $index }}:
    type: OS::Cinder::Volume
    properties:
      size: {{ $v.volume_size }}
      volume_type: {{ $v.volume_type }}
      availability_zone: {{ $.server.availability_zone }}
{{ end }}

  port{{ $intindex }}:
    type: 'OS::Neutron::Port'
    properties:
      network: {{ $.server.subnet.network_name }}
      replacement_policy: AUTO
{{ if $.server.security_groups }}
      security_groups:
{{ range $index, $v := $.server.security_groups }}
        - {{ $v }}
{{ end }}
{{ end }}
      fixed_ips:
        - subnet: {{ $.server.subnet.subnet_id }}

  node{{ $intindex }}:
    type: OS::Nova::Server
    properties:
      name: {{ $.server.name }}-{{ $intindex }}
      flavor: {{ $.server.flavor }}
{{ if $.server.key_name }}
      key_name: {{ $.server.key_name }}
{{ end }}
{{ if $.server.admin_pass }}
      admin_pass: {{ $.server.admin_pass }}
{{ end }}
{{ if $.server.user_data }}
      user_data: |-
{{ indent 8 $.server.user_data }}
{{ end }}
      networks:
        - port:
            get_resource: port{{ $intindex }}
      availability_zone: {{ $.server.availability_zone }}
      block_device_mapping_v2:
        - boot_index: 0
{{ if $.server.boot_volume_id }}
          volume_id: {{ $.server.boot_volume_id }}
{{ else }}
          volume_id: {get_resource: boot_volume{{ $intindex }}}
          delete_on_termination: {{ $.server.boot_volume.volume_delete }}
{{ end }}

{{ range $index, $v := $.server.volumes }}
        - boot_index: {{ add 1 $index }}
          volume_id: {get_resource: node{{ $intindex }}data_volume{{ $index }}}
          delete_on_termination: {{ $v.volume_delete }}
{{ end }}


{{ if $.server.security_groups }}
      security_groups:
{{ range $index, $v := $.server.security_groups }}
        - {{ $v }}
{{ end }}
{{ end }}

{{ end }}

