# CNI Binary for Dunlin Plugin 
This CNI plugin was created in order to further popularize Open vSwitch based Kubernetes cluster networking.

This simple CNI binary connects containers / pods directly to an
OVS bridge called br0, which eliminates the usage of any Linux Bridge,
and opens up the possiblities for high-level software defined networking.

To use the Dunlin Plugin in your Kubernetes cluster, before the installation of the network plugin (refer to step #3 at https://kubernetes.io/docs/setup/independent/create-cluster-kubeadm/) install Open vSwitch on every node using the following command:

    $ sudo apt install openvswitch-switch
    
You can veryfy that OVS is up and running by the following command:

    $ sudo ovs-vsctl show
    
Then, you can install the Dunlin Plugin with the following Kubernetes command:

    $ kubectl apply -f https://dunlin.io/dunlin.yaml
    
    
