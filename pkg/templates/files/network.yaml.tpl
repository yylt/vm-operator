heat_template_version: 2016-10-14

description: >
  This template will create network resources

parameters:

{% if existing_subnet %}
  existing_network:
    type: string
    default: ""

  existing_subnet:
    type: string
    default: ""
{% else %}
  private_network_cidr:
    type: string
    description: network range for fixed ip network

  private_network_name:
    type: string
    description: fixed network name
    default: ""
{% endif %}

{% if floating_ip == "enable" %}
  external_network:
    type: string
    description: uuid/name of a network to use for floating ip addresses
{% endif %}

  neutron_az:
    type: comma_delimited_list
    description: neutron availability zone

resources:

{% if not existing_subnet %}
  fixed_network:
    type: OS::Neutron::Net
    properties:
      name: {get_param: private_network_name}
      availability_zone_hints: {get_param: neutron_az }

  fixed_subnet:
    type: OS::Neutron::Subnet
    properties:
      cidr: {get_param: private_network_cidr}
      network: {get_resource: private_network}
      dns_nameservers: {get_param: dns_nameserver}
{% endif %}

{% if floating_ip == "enable" %}
  extrouter:
    type: OS::Neutron::Router
    properties:
      external_gateway_info:
        network: {get_param: external_network}

  extrouter_inside:
    type: OS::Neutron::RouterInterface
    properties:
      router_id: {get_resource: extrouter}
    {% if existing_subnet %}
      subnet: {get_resource: existing_subnet}
    {% else %}
      subnet: {get_resource: fixed_subnet}
    {% endif %}
{% endif %}

outputs:

    fixed_network:
      description: >
        Network ID where to provision machines
    {% if existing_subnet %}
      value: {get_param: existing_network}
    {% else %}
      value: {get_resource: fixed_network}
    {% endif %}

    fixed_subnet:
      description: >
        Subnet ID where to provision machines
    {% if existing_subnet %}
      value: {get_param: existing_subnet}
    {% else %}
      value: {get_resource: fixed_subnet}
    {% endif %}