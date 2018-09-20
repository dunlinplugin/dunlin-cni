package main

import (
	// originally imported from github.com/containernetworking/cni/pkg
	"skel"
	"version"
	"flag"
	"fmt"
    "encoding/json"
    "strings"
	"os/exec"
	"log"
	"io"
	"os"
	"strconv"
	"net"
	"regexp"
)

var (
	logpath = flag.String("logpath", "/var/log/dunlin/cni.log", "Log Path")
)

var logger = log.New(os.Stderr, "", 0)

func cmdAdd(args *skel.CmdArgs) error {
	var result error
	// call the ipam plugin
	plugin := "host-local"
	path := "/opt/cni/bin"
	config_string := B2S(args.StdinData)
	ipam_output := call_ipam_plugin(plugin, path, config_string)

	// TODO: the complete conf file should be processed in a dedicated function into a dedicated structure...
	var config_json map[string]interface{}
	json.Unmarshal([]byte(config_string), &config_json)
	bridge := config_json["bridge"].(string)

	// get ip, cidr and gw from the ipam output
	var ipam_output_json map[string]interface{}
	json.Unmarshal([]byte(ipam_output), &ipam_output_json)
	ip, cidr, gw := get_ip_cidr_gw_from_ipam_result(ipam_output_json)

	create_ovs_bridge("br0", gw, cidr)
	container_pid := strings.Split(args.Netns, "/")[2]
	create_netns_link(container_pid)
	ovs_port, container_port := create_veth_pair(args.ContainerID)
	of_port_num := strings.Split(ip, ".")[3] // 4th octet of the IP
	set_up_port_inside_host(bridge, ovs_port, of_port_num)
	set_up_port_inside_container(container_port, container_pid, args.IfName, ip, cidr, gw)
	delete_netns_link(container_pid)
	// dump json to stdout
	fmt.Printf("%s\n", ipam_output)

	return result

}

func cmdDel(args *skel.CmdArgs) error {
	var result error

	// TODO: should be "wrapped" in order this part to be presenet only once in code
	config_string := B2S(args.StdinData)
	var config_json map[string]interface{}
	json.Unmarshal([]byte(config_string), &config_json)
	bridge := config_json["bridge"].(string)
	network_name := config_json["name"].(string)

	bridge_ip := get_bridge_ip(bridge)
	delete_ip_host_file(bridge, network_name, bridge_ip, args.ContainerID)
	delete_ovs_container_port(bridge, args.ContainerID)
	// dump json to stdout
	fmt.Print("{\"cniVersion\": \"0.2.0\"}\n")

	return result

}

func delete_ovs_container_port(bridge string, container_id string) {
	id := container_id[len(container_id)-8:] //last 8 digit
	ovs_port := "veth_" + id
	cmd := exec.Command("ovs-vsctl", "del-port", bridge, ovs_port)
	cmd.Run()
}


func delete_ip_host_file(bridge string, network_name string, ip string, container_id string) {
	id := container_id[len(container_id)-8:] //last 8 digit
	ovs_port := "veth_" + id

	cmd := exec.Command("ovs-vsctl", "get", "interface", ovs_port, "ofport")
	out, _ := cmd.Output()
	of_port_num := strings.Replace(string(out), "\n", "", -1)

	re := regexp.MustCompile("([0-9]+.[0-9]+.[0-9]+.)")
	del_ip := re.FindStringSubmatch(ip)[1]
	del_ip += string(of_port_num) // of_port_num equals to the 4th octet of the container IP

	path := string("/var/lib/cni/networks" + "/" + network_name + "/" + del_ip)
	err := os.Remove(path)
	//err := os.Remove("/var/lib/cni/networks" + "/" + network_name + "/" + del_ip[0] + "." + "" + del_ip[1] + "." + del_ip[2] + "." + of_port_num)
	if err != nil {
		fmt.Printf("delete_ip_host_file: %v\n", err)
	}
}


func delete_netns_link(pid string) {
	cmd := exec.Command("rm", "-f", "/var/run/netns/" + pid)
	err := cmd.Run()
	if err != nil {
		fmt.Printf("delete_netns_link: %v\n", err)
	}
}

func set_up_port_inside_container(port string, pid string, iface string, ip string, cidr string, gw string){
	cmd := exec.Command("ip", "link", "set", port, "netns", pid)
	cmd.Run()
	cmd = exec.Command("ip", "netns", "exec", pid, "ip", "link", "set", "dev", port, "name", iface)
	cmd.Run()
	cmd = exec.Command("ip", "netns", "exec", pid, "ip", "link", "set", iface, "address", ip2mac(ip))
	cmd.Run()
	cmd = exec.Command("ip", "netns", "exec", pid, "ip", "link", "set", "dev", iface, "mtu", strconv.Itoa(get_mtu_size() - 50))
	cmd.Run()
	cmd = exec.Command("ip", "netns", "exec", pid, "ip", "link", "set", iface, "up")
	cmd.Run()
	cmd = exec.Command("ip", "netns", "exec", pid, "ip", "addr", "add", ip + "/" + cidr, "dev", iface)
	cmd.Run()
	cmd = exec.Command("ip", "netns", "exec", pid, "ip", "route", "add", "default", "via", gw)
	cmd.Run()
	cmd = exec.Command("ip", "netns", "exec", pid, "arp", "-s", gw, ip2mac(gw))
	cmd.Run()
}

func set_up_port_inside_host(bridge string, port string, of_port_num string) {
	cmd := exec.Command("ip", "link", "set", port, "up")
	cmd.Run()

	cmd = exec.Command("ovs-vsctl", "add-port", bridge, port, "--", "set", "interface", port, "ofport_request=" + of_port_num)
	err := cmd.Run()
	if err != nil {
		logger.Printf("set_up_port_inside_host: %v", err)
		os.Exit(0)
	}
}

func create_veth_pair(container_id string) (string, string) {
	id := container_id[len(container_id)-8:] //last 8 digit
	ovs_port := "veth_" + id
	container_port := "veth_" + id + "_c"
	cmd := exec.Command("ip", "link", "add", ovs_port, "type", "veth", "peer", "name", container_port)
	cmd.Run()
	return ovs_port, container_port
}

func get_ip_cidr_gw_from_ipam_result(config map[string]interface{}) (string, string, string) {
	ip_and_cidr := config["ip4"].(map[string]interface{})["ip"].(string)
	ip := strings.Split(ip_and_cidr, "/")[0]
	cidr := strings.Split(ip_and_cidr, "/")[1]
	gw := config["ip4"].(map[string]interface{})["gateway"].(string)

	return ip, cidr, gw
}

func create_ovs_bridge(bridge string, gw string, cidr string) {
	cmd := exec.Command("ovs-vsctl", "br-exists", bridge)
	err := cmd.Run()
	if err != nil {
		logger.Printf("bridge %s does not exist\n", bridge)
		logger.Printf("creating bridge %s\n", bridge)

		cmd = exec.Command("ovs-vsctl", "add-br", bridge)
		err = cmd.Run()

		if err != nil {
			logger.Printf("create_ovs_bridge: %v\n", err)
			os.Exit(0)
		}
		logger.Printf("bridge %s is created\n", bridge)
	}

	cmd = exec.Command("ifconfig", bridge, gw + "/" + cidr, "up")
	cmd.Run()
	cmd = exec.Command("ovs-vsctl", "add-port", bridge, "vxlan0", "--", "set", "interface", "vxlan0", "type=vxlan", "option:key=flow", "option:remote_ip=flow")
	cmd.Run()
	cmd = exec.Command("ovs-vsctl", "set-controller", bridge, "ptcp:16633")
	cmd.Run()
	cmd = exec.Command("ovs-vsctl", "set", "bridge", bridge, "other-config:hwaddr=\"" + ip2mac(gw) + "\"")
	cmd.Run()

    //new rules for POD --> Node communication
    globalPodCidr := "10.244.0.0/16" //to be taken out as env variable
	cmd = exec.Command("ip", "route", "add", globalPodCidr, "via", ip2fakeGateway(gw), "dev", bridge)
	cmd.Run()
	cmd = exec.Command("arp", "-s", ip2fakeGateway(gw), ip2mac(ip2fakeGateway(gw)))
	cmd.Run()
	cmd = exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", globalPodCidr, "!", "-d", "224.0.0.0/4", "-j", "MASQUERADE")
	cmd.Run()
    cmd = exec.Command("iptables", "-t", "filter", "-A", "FORWARD", "-s", globalPodCidr, "-j", "ACCEPT")
	cmd.Run()
    cmd = exec.Command("iptables", "-t", "filter", "-A", "FORWARD", "-d", globalPodCidr, "-j", "ACCEPT")
	cmd.Run()
        
}

func ip2fakeGateway(ip string) string {
	re_inside := regexp.MustCompile(`\.\d+$`)
	result := re_inside.ReplaceAllString(ip, ".254")	
    
    return result
}

func ip2mac(ip string) string {
	mac_address := "0a:58"

	for _, item := range strings.Split(ip, ".") {
		dec, _ := strconv.Atoi(item)

		hex := fmt.Sprintf("%x", dec)

		if dec < 16 {
			mac_address += ":0" + hex
		} else {
			mac_address += ":" + hex
		}
	}
	return mac_address
}

func remove_multiple_spaces(text string) string {
	re_inside_whtsp := regexp.MustCompile(`[\s\p{Zs}]{2,}`)
	result := re_inside_whtsp.ReplaceAllString(text, " ")

	return result
}

func get_mtu_size() int{
	cmd := exec.Command("route")
	out, err := cmd.Output()
	route_table := string(out)
	if err != nil {
		logger.Printf("get_mtu_size: %v", err)
		os.Exit(0)
	}

	routes := strings.Split(route_table, "\n")
	var default_gw_if string
	for i := 0; i < len(routes); i++ {
		if strings.Contains(routes[i], "default") {
			row := remove_multiple_spaces(routes[i])
			columns := strings.Split(row, " ")
			default_gw_if = columns[len(columns)-1]
		}
	}

	itf, _ := net.InterfaceByName(default_gw_if)
    //logger.Printf("set_up_port_inside_host: %d", itf.MTU)
	return itf.MTU
}

func call_ipam_plugin(plugin string, path string, config string) []uint8{
	//cmd := exec.Command("/opt/cni/bin/host-local")
	cmd := exec.Command(path + "/" + plugin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		logger.Printf("call_ipam_plugin: %v", err)
		os.Exit(0)
	}

	go func() {
		defer stdin.Close()
		io.WriteString(stdin, config)
	}()

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatal(err)
	}

	//fmt.Printf("\n\n A visszakapott ipam kimenet")
	//fmt.Printf("%s\n", out)

	return out
}

func create_netns_link(id string) {
	cmd := exec.Command("mkdir", "-p", "/var/run/netns")
	cmd.Run()
	if _, err := os.Stat("/var/run/netns/" + id); err == nil {

	} else {
		cmd = exec.Command("ln", "-s", "/proc/" + id + "/ns/net", "/var/run/netns/" + id)
		cmd.Run()
	}
}


// []byte to string conversion
func B2S(bs []byte) string {
	b := make([]byte, len(bs))
	for i, v := range bs {
		b[i] = byte(v)
	}
	return string(b)
}

func get_bridge_ip(bridge string) string {
	cmd := exec.Command("ifconfig", bridge)
	out, _ := cmd.Output()
	if_config := string(out)

	//alma := "inet addr:10.0.2.7  Bcast:10.0.2.255  Mask:255.255.255.0"
	re := regexp.MustCompile("inet addr:([0-9]+.[0-9]+.[0-9]+.[0-9]+)")
	ip := re.FindStringSubmatch(if_config)[1]

	fmt.Printf("---------IP: %s", ip)

	return ip
}

func main() {
	skel.PluginMain(cmdAdd, cmdDel, version.All)
/*
	flag.Parse()
	utils.NewLog(*logpath)
	utils.Log.Println("hello")
	for i := 0; i < 10; i++ {
		utils.Log.Println(i)
	}
*/
}

//gwp, _ := gateway.DiscoverGateway()
//fmt.Printf("The gateway is: %s", gwp)

/*
// INTERFACE INFO WITH NETLINK:
gwp, err := netlink.RouteList(nil, nl.FAMILY_V4)
if err != nil {
	logger.Printf("Baj van: %v", err)
}
fmt.Printf("\n------------------------\n")
for i := 0; i < len(gwp); i++ {
	fmt.Printf("\n%d: %s %s", gwp[i].MTU, gwp[i].Gw, gwp[i].Src)
}
fmt.Printf("\n------------------------\n")

// INTERFACE INFO WITH NET:
fmt.Printf("\n------------------------\n")
ift, _ := net.Interfaces()
for i := 0; i < len(ift); i++ {
	fmt.Printf("%s --> %s --> %d", ift[i].Name, ift[i].HardwareAddr, ift[i].MTU)
}
*/