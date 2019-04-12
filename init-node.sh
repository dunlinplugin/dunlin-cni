#/bin/bash 

#get class C IP prefix from env
CLASSC=`echo $IPOFIFACE | sed 's/\.[0-9]*$//'`
#get last octet of the IP address of the default interface
LASTOCTET=`ip address show | grep $CLASSC | grep 'inet ' | head -1 | sed 's/.*inet //' | sed 's/\/.*//' | sed 's/.*\.//'`

#fallback to default interface if LASTOCTET is empty
if [ -z "$LASTOCTET" ]
then
    #get default interface
    DEV=`ip route show | head -1 | sed 's/.*dev //' | sed 's/ .*//'`
    #get last octet of the IP address of the default interface
    LASTOCTET=`ip address show dev $DEV | grep 'inet ' | head -1 | sed 's/.*inet //' | sed 's/\/.*//' | sed 's/.*\.//'`
fi

#Hexa numbers
HEX=`printf "0%x" $LASTOCTET`
if [ $LASTOCTET -ge 16 ]; then HEX=`printf "%x" $LASTOCTET`; fi
echo "hex string is: $HEX"

echo "last octet the IP address in dev $DEV is $LASTOCTET" 

echo "{
    \"cniVersion\": \"0.2.0\",
    \"name\": \"dunlin\",
    \"type\": \"dunlin\",
    \"bridge\": \"br0\",
    \"isGateway\": true,
    \"ipMasq\": true,
    \"ipam\": {
        \"type\": \"host-local\",
        \"subnet\": \"10.244.$LASTOCTET.0/24\",
        \"routes\": [
            { \"dst\": \"0.0.0.0/0\" },
            { \"dst\": \"1.1.1.1/32\", \"gw\":\"10.244.$LASTOCTET.1\"}
        ]
    }
}" >/etc/cni/net.d/01-dunlin.conf

#copy the plugin into /opt/cni/bin/
cp dunlin /opt/cni/bin/dunlin

#set up OVS bridge
ovs-vsctl set-manager ptcp:6640 
ovs-vsctl add-br br0
ovs-vsctl set-controller br0 ptcp:16633
ovs-vsctl add-port br0 vxlan0 -- set interface vxlan0 type=vxlan option:key=flow option:remote_ip=flow
ovs-vsctl set bridge br0 other-config:hwaddr=\"0a:58:0a:f4:$HEX:01\"

#set IP and MAC address of OVS bridge
ip link set dev br0 up
ip addr add 10.244.$LASTOCTET.1/24 dev br0

#set routing to other PODs with a static gateway with static ARP
#this is for communication of hosts with PODs on other nodes (e.g. DNS runs on a worker and wants to communicate with the k8s api on master node with it's host IP)
ip route add 10.244.0.0/16 via 10.244.$LASTOCTET.254 dev br0
ip neigh add 10.244.$LASTOCTET.254 lladdr 0a:58:0a:f4:$HEX:fe dev br0 nud permanent

#register IP address into Kubernetes master
#HOSTNAME=`hostname`
#kubectl patch node $HOSTNAME -p '{\"spec\":{\"podCIDR\":\"10.244.$LASTOCTET.0/24\"}}'

exit 0