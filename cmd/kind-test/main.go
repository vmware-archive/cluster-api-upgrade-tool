package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/vmware/cluster-api-upgrade-tool/pkg/kind/actions"
)

func main() {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Here we go!")
	clusterName := "my-cluster"
	for {
		// read input
		text, _ := reader.ReadString('\n')
		cleanText := strings.TrimSpace(text)
		inputs := strings.Split(cleanText, " ")
		switch inputs[0] {
		case "new-cluster":
			fmt.Println("Creating load balancer")
			if err := actions.SetUpLoadBalancer(clusterName); err != nil {
				panic(err)
			}
			if err := actions.CreateControlPlane(clusterName); err != nil {
				panic(err)
			}
		case "add-worker":
			if err := actions.AddWorker(clusterName); err != nil {
				panic(err)
			}
		case "delete-node":
			if len(inputs) < 2 {
				fmt.Println("usage: delete-worker my-cluster-worker1")
				continue
			}
			if err := actions.DeleteNode(clusterName, inputs[1]); err != nil {
				panic(err)
			}
		case "add-control-plane":
			if err := actions.AddControlPlane(clusterName); err != nil {
				panic(err)
			}
		case "set-cluster-name":
			fmt.Println("setting cluster name...")
			clusterName = inputs[1]
		default:
			fmt.Println("Unknown command")
		}
		fmt.Println("Done!")
	}
}
