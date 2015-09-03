package main

import (
	"fmt"
	"math/big"

	"github.com/spf13/cobra"

	"github.com/NebulousLabs/Sia/modules"
)

var (
	hostCmd = &cobra.Command{
		Use:   "host",
		Short: "Perform host actions",
		Long:  "View or modify host settings. Modifying host settings also announces the host to the network.",
		Run:   wrap(hoststatuscmd),
	}

	hostConfigCmd = &cobra.Command{
		Use:   "config [setting] [value]",
		Short: "Modify host settings",
		Long: `Modify host settings.
Available settings:
	totalstorage
	minfilesize
	maxfilesize
	minduration
	maxduration
	windowsize
	price (in SC per GB per month)
	collateral`,
		Run: wrap(hostconfigcmd),
	}

	hostAnnounceCmd = &cobra.Command{
		Use:   "announce",
		Short: "Announce yourself as a host",
		Long: `Announce yourself as a host on the network.
You may also supply a specific address to be announced, e.g.:
	siac host announce my-host-domain.com:9001
Doing so will override the standard connectivity checks.`,
		Run: hostannouncecmd,
	}

	hostStatusCmd = &cobra.Command{
		Use:   "status",
		Short: "View host settings",
		Long:  "View host settings, including available storage, price, and more.",
		Run:   wrap(hoststatuscmd),
	}
)

func hostconfigcmd(param, value string) {
	// convert price to hastings/byte/block
	if param == "price" {
		p, ok := new(big.Rat).SetString(value)
		if !ok {
			fmt.Println("could not parse price")
			return
		}
		p.Mul(p, big.NewRat(1e24/1e9, 4320))
		value = new(big.Int).Div(p.Num(), p.Denom()).String()
	}
	err := post("/host/configure", param+"="+value)
	if err != nil {
		fmt.Println("Could not update host settings:", err)
		return
	}
	fmt.Println("Host settings updated.")
}

func hostannouncecmd(cmd *cobra.Command, args []string) {
	var err error
	switch len(args) {
	case 0:
		err = post("/host/announce", "")
	case 1:
		err = post("/host/announce", "address="+args[0])
	default:
		cmd.Usage()
		return
	}
	if err != nil {
		fmt.Println("Could not announce host:", err)
		return
	}
	fmt.Println("Host announcement submitted to network.")
}

func hoststatuscmd() {
	info := new(modules.HostInfo)
	err := getAPI("/host/status", info)
	if err != nil {
		fmt.Println("Could not fetch host settings:", err)
		return
	}
	// convert price to SC/GB/mo
	price := new(big.Rat).SetInt(info.Price.Big())
	price.Mul(price, big.NewRat(4320, 1e24/1e9))
	fmt.Printf(`Host settings:
Storage:      %v (%v used)
Price:        %v SC per GB per month
Collateral:   %v
Max Filesize: %v
Max Duration: %v
Contracts:    %v
`, filesizeUnits(info.TotalStorage), filesizeUnits(info.TotalStorage-info.StorageRemaining),
		price.FloatString(3), info.Collateral, info.MaxFilesize, info.MaxDuration, info.NumContracts)
}
