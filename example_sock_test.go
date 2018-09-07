// +build linux

package ebpf_test

import (
	"flag"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/newtools/ebpf"
)

type IPHdr struct {
	VersionIHL     uint8
	Tos            uint8
	Length         uint16
	ID             uint16
	Flags          uint8
	FragmentOffset uint16
	TTL            uint8
	Protocol       uint8
	Checksum       uint16
	SrcIP          [4]byte
	DestIP         [4]byte
	PayloadLength  int
}

const EthHLen = 14

// ExampleSocket demonstrates how to attach an EBPF program
// to a socket.
func Example_socket() {
	const SO_ATTACH_BPF = 50

	index := flag.Int("index", 0, "specify ethernet index")
	flag.Parse()
	bpfMap, err := ebpf.NewMap(&ebpf.MapSpec{
		Type:       ebpf.Array,
		KeySize:    4,
		ValueSize:  8,
		MaxEntries: 256,
	})
	if err != nil {
		panic(err)
	}
	ip := IPHdr{}
	mapFd := bpfMap.FD()
	ebpfInss := ebpf.Instructions{
		// save context for previous caller
		// mov r1, r6
		ebpf.BPFIDstSrc(ebpf.MovSrc, ebpf.Reg6, ebpf.Reg1),
		// get ip protocol
		// ldb r0, *(mem + off)
		ebpf.BPFIImm(ebpf.LdAbsB, int32(EthHLen+unsafe.Offsetof(ip.Protocol))),
		// set 4 bytes off the frame pointer to be equal to r0
		// stxw [rfp+off], src
		ebpf.BPFIDstOffSrc(ebpf.StXW, ebpf.RegFP, ebpf.Reg0, -4),
		// set 2nd arg (to be givent to map fx below) to current FP
		// mov r2, rfp
		ebpf.BPFIDstSrc(ebpf.MovSrc, ebpf.Reg2, ebpf.RegFP),
		// subtract 4 from reg2
		// sub r2, 4
		ebpf.BPFIDstImm(ebpf.AddImm, ebpf.Reg2, -4),
		// load the map fd into memory, in argument 1 position
		// lddw reg1, (*:from_user_space)(imm)
		ebpf.BPFILdMapFd(ebpf.Reg1, mapFd),
		// call map lookup -> map_lookup_elem(r1, r2)
		// call imm
		ebpf.BPFCall(ebpf.MapLookupElement),
		// exit if reg0 is 0
		// jeq r0, 2, 0
		ebpf.BPFIDstOff(ebpf.JEqImm, ebpf.Reg0, 2),
		// load int 1 into r1 register
		// mov r1, 1
		ebpf.BPFIDstImm(ebpf.MovImm, ebpf.Reg1, 1),
		// atomically increment regsiter
		// xaddst r0, imm
		ebpf.BPFIDstSrc(ebpf.XAddStSrc, ebpf.Reg0, ebpf.Reg1),
		// set exit code to 0
		// mov r0, imm
		ebpf.BPFIDstImm(ebpf.MovImm, ebpf.Reg0, 0),
		// exit
		ebpf.BPFIOp(ebpf.Exit),
	}
	bpfProgram, err := ebpf.NewProgram(&ebpf.ProgramSpec{
		Type:         ebpf.SocketFilter,
		License:      "GPL",
		Instructions: ebpfInss,
	})

	if err != nil {
		fmt.Printf("%s\n", ebpfInss)
		panic(err)
	}
	sock, err := openRawSock(*index)
	if err != nil {
		panic(err)
	}
	if err := syscall.SetsockoptInt(sock, syscall.SOL_SOCKET, SO_ATTACH_BPF, bpfProgram.FD()); err != nil {
		panic(err)
	}
	fmt.Printf("Filtering on eth index: %d\n", *index)
	fmt.Println("Packet stats:")
	for {
		const (
			ICMP = 0x01
			TCP  = 0x06
			UDP  = 0x11
		)

		time.Sleep(time.Second)
		var icmp uint64
		var tcp uint64
		var udp uint64
		ok, err := bpfMap.Get(uint32(ICMP), &icmp)
		if err != nil {
			panic(err)
		}
		if !ok {
			icmp = 0
		}
		ok, err = bpfMap.Get(uint32(TCP), &tcp)
		if err != nil {
			panic(err)
		}
		if !ok {
			tcp = 0
		}
		ok, err = bpfMap.Get(uint32(UDP), &udp)
		if err != nil {
			panic(err)
		}
		if !ok {
			udp = 0
		}
		fmt.Printf("\r\033[m\tICMP: %d TCP: %d UDP: %d", icmp, tcp, udp)
	}
}

func openRawSock(index int) (int, error) {
	const ETH_P_ALL uint16 = 0x00<<8 | 0x03
	sock, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW|syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC, int(ETH_P_ALL))
	if err != nil {
		return 0, err
	}
	sll := syscall.SockaddrLinklayer{}
	sll.Protocol = ETH_P_ALL
	sll.Ifindex = index
	if err := syscall.Bind(sock, &sll); err != nil {
		return 0, err
	}
	return sock, nil
}
