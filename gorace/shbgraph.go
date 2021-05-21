package main

import (
	"github.com/april1989/origin-go-tools/go/ssa"
	log "github.com/sirupsen/logrus"
	"github.com/twmb/algoimpl/go/graph"
)

//bz: contains hb graph related functions; tmp move it here, will update soon

// self-defined queue for traversing Happens-Before Graph
type queue struct {
	data []graph.Node
}

func (q *queue) enQueue(v graph.Node) {
	q.data = append(q.data, v)
}
func (q *queue) deQueue() graph.Node {
	v := q.data[0]
	q.data = q.data[1:]
	return v
}
func (q *queue) isEmpty() bool {
	return len(q.data) == 0
}
func (q *queue) size() int {
	return len(q.data)
}


// reachable determines if 2 input instructions are connected in the Happens-Before Graph
func (a *analysis) reachable(fromIns ssa.Instruction, fromGo int, toIns ssa.Instruction, toGo int) bool {
	fromInsKey := goIns{ins: fromIns, goID: fromGo}
	toInsKey := goIns{ins: toIns, goID: toGo}
	fromNode := a.RWinsMap[fromInsKey] // starting node
	toNode := a.RWinsMap[toInsKey]     // target node

	//use breadth-first-search to traverse the Happens-Before Graph
	var visited []graph.Node
	q := &queue{}
	q.enQueue(fromNode)
	for !q.isEmpty() {
		for size := q.size(); size > 0; size-- {
			node := q.deQueue()
			if node == toNode {
				return true
			}
			for _, neighbor := range a.HBgraph.Neighbors(node) {
				if sliceContainsNode(visited, neighbor) {
					continue
				}
				visited = append(visited, neighbor)
				q.enQueue(neighbor)
			}
		}
	}
	return false
}

func (a *analysis) buildHB() {
	var prevN graph.Node
	var selectN []graph.Node
	var readyCh []string
	var selCaseEndN []graph.Node
	var ifN []graph.Node
	var ifSuccEndN []graph.Node
	goCaller := make(map[*ssa.Go]graph.Node)
	waitingN := make(map[goIns]graph.Node)
	chanRecvs := make(map[string]graph.Node) // map channel name to graph node
	chanSends := make(map[string]graph.Node) // map channel name to graph node
	for nGo, insSlice := range a.RWIns {
		for i, eachIns := range insSlice {
			anIns := eachIns.ins
			disjoin := false // detach select case statement from subsequent instruction
			insKey := goIns{ins: anIns, goID: nGo}
			if nGo == 0 && i == 0 { // main goroutine, first instruction
				prevN = a.HBgraph.MakeNode() // initiate for future nodes
				*prevN.Value = insKey
				if goInstr, ok := anIns.(*ssa.Go); ok {
					goCaller[goInstr] = prevN // sequentially store go calls in the same goroutine
				}
			} else {
				currN := a.HBgraph.MakeNode()
				*currN.Value = insKey
				if nGo != 0 && i == 0 { // worker goroutine, first instruction
					prevN = goCaller[anIns.(*ssa.Go)] // first node in subroutine
				} else if goInstr, ok := anIns.(*ssa.Go); ok { // spawning of subroutine
					//if _, ok1 := goCaller[goInstr]; ok1 { // repeated spawning in loop
					//	continue
					//}
					goCaller[goInstr] = currN // store go calls in the same goroutine
				} else if selIns, ok1 := anIns.(*ssa.Select); ok1 {
					selectN = append(selectN, currN) // select node
					readyCh = a.selReady[selIns]
					selCaseEndN = []graph.Node{} // reset slice of nodes when encountering multiple select statements
					readys := 0
					for ith, ch := range readyCh {
						if ith < len(selIns.States) && ch != "" && selIns.States[ith].Dir == 1 { // TODO: readyCh may be longer than selIns.States?
							readys++
							if _, ok0 := a.selUnknown[selIns]; ok0 && readys == 1 {
								chanSends[ch] = currN
							}
						}
					}
				} else if ins, chR := anIns.(*ssa.UnOp); chR {
					if ch := a.getRcvChan(ins); ch != "" { // a channel receive Op
						chanRecvs[a.getRcvChan(ins)] = currN
						if a.isReadySel(ch) { // channel waited on by select
							disjoin = true // no edge between current node and node of succeeding instruction
						}
					}
				} else if insS, chS := anIns.(*ssa.Send); chS {
					chanSends[a.getSndChan(insS)] = currN
				} else if _, isIf := anIns.(*ssa.If); isIf {
					ifN = append([]graph.Node{currN}, ifN...) // store if statements
				}
				if ch, ok0 := a.selectCaseEnd[anIns]; ok0 && sliceContainsStr(readyCh, ch) {
					selCaseEndN = append(selCaseEndN, currN)
				}
				if _, isSuccEnd := a.ifSuccEnd[anIns]; isSuccEnd {
					ifSuccEndN = append(ifSuccEndN, currN)
				}
				// edge manipulation:
				if ch, ok := a.selectCaseBegin[anIns]; ok && channelComm && selectN != nil {
					if ch == "defaultCase" || ch == "timeOut" {
						err := a.HBgraph.MakeEdge(selectN[0], currN) // select node to default case
						if err != nil {
							log.Fatal(err)
						}
					} else {
						if _, ok1 := chanRecvs[ch]; ok1 {
							err := a.HBgraph.MakeEdge(chanRecvs[ch], currN) // receive Op to ready case
							if err != nil {
								log.Fatal(err)
							}
						} else if sliceContainsStr(readyCh, ch) {
							err := a.HBgraph.MakeEdge(selectN[0], currN) // select node to assumed ready cases
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				} else if _, ok1 := a.selectDone[anIns]; ok1 && channelComm && selectN != nil {
					if len(selCaseEndN) > 1 { // more than one portal is ready
						err := a.HBgraph.MakeEdge(selectN[0], currN) // select statement to select done
						if err != nil {
							log.Fatal(err)
						}
					} else if len(selCaseEndN) > 0 {
						err := a.HBgraph.MakeEdge(selCaseEndN[0], currN) // ready case to select done
						if err != nil {
							log.Fatal(err)
						}
					}
					if len(selectN) > 1 {
						selectN = selectN[1:]
					} // completed analysis of one select statement
				} else if ifInstr, ok2 := a.ifSuccBegin[anIns]; ok2 {
					skipSucc := false
					for beginIns, ifIns := range a.ifSuccBegin {
						if ifIns == ifInstr && beginIns != anIns && sliceContainsInsAt(a.commIfSucc, beginIns) != -1 && channelComm { // other succ contains channel communication
							if (anIns.Block().Comment == "if.then" && beginIns.Block().Comment == "if.else") || (anIns.Block().Comment == "if.else" && beginIns.Block().Comment == "if.then") {
								skipSucc = true
								a.omitComm = append(a.omitComm, anIns.Block())
							}
						}
					}
					if !skipSucc && ifN != nil {
						err := a.HBgraph.MakeEdge(ifN[0], currN)
						if err != nil {
							log.Fatal(err)
						}
					}
				} else {
					err := a.HBgraph.MakeEdge(prevN, currN)
					if err != nil {
						log.Fatal(err)
					}
				}
				if !disjoin {
					prevN = currN
				}
			}
			// Create additional edges:
			if a.isReadIns(anIns) || isWriteIns(anIns) {
				a.RWinsMap[insKey] = prevN
			} else if callIns, ok := anIns.(*ssa.Call); ok { // taking care of WG operations. TODO: identify different WG instances
				if callIns.Call.Value.Name() == "Wait" {
					waitingN[insKey] = prevN // store Wait node for later edge creation TO this node
				} else if callIns.Call.Value.Name() == "Done" {
					for wKey, wNode := range waitingN {
						if a.sameAddress(callIns.Call.Args[0], wKey.ins.(*ssa.Call).Call.Args[0], nGo, wKey.goID) &&
							(*(prevN.Value)).(goIns).goID != (*(wNode.Value)).(goIns).goID {
							err := a.HBgraph.MakeEdge(prevN, wNode) // create edge from Done node to Wait node
							var fromName string
							var toName string
							if nGo == 0 {
								fromName = a.entryFn
							} else {
								fromName = a.goNames(a.RWIns[nGo][0].ins.(*ssa.Go))
							}
							if (*wNode.Value).(goIns).goID == 0 {
								toName = a.entryFn
							} else {
								toName = a.goNames(a.RWIns[(*wNode.Value).(goIns).goID][0].ins.(*ssa.Go))
							}
							if DEBUGHBGraph {
								log.Debug("WaitGroup edge from Goroutine ", fromName, " [", nGo, "] to Goroutine ", toName, " [", (*wNode.Value).(goIns).goID, "]")
							}
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				}
			} else if dIns, ok1 := anIns.(*ssa.Defer); ok1 {
				if dIns.Call.Value.Name() == "Done" {
					for wKey, wNode := range waitingN {
						if a.sameAddress(dIns.Call.Args[0], wKey.ins.(*ssa.Call).Call.Args[0], nGo, wKey.goID) &&
							(*(prevN.Value)).(goIns).goID != (*(wNode.Value)).(goIns).goID {
							err := a.HBgraph.MakeEdge(prevN, wNode) // create edge from Done node to Wait node
							var fromName string
							var toName string
							if nGo == 0 {
								fromName = a.entryFn
							} else {
								fromName = a.goNames(a.RWIns[nGo][0].ins.(*ssa.Go))
							}
							if (*wNode.Value).(goIns).goID == 0 {
								toName = a.entryFn
							} else {
								toName = a.goNames(a.RWIns[(*wNode.Value).(goIns).goID][0].ins.(*ssa.Go))
							}
							if DEBUGHBGraph {
								log.Debug("WaitGroup edge from Goroutine ", fromName, " [", nGo, "] to Goroutine ", toName, " [", (*wNode.Value).(goIns).goID, "]")
							}
							if err != nil {
								log.Fatal(err)
							}
						}
					}
				}
			}
			if sendIns, ok := anIns.(*ssa.Send); ok && channelComm { // detect matching channel send operations
				for ch, sIns := range a.chanSnds {
					if rcvN, matching := chanRecvs[ch]; matching && sliceContainsSnd(sIns, sendIns) &&
						(*(prevN.Value)).(goIns).goID != (*(rcvN.Value)).(goIns).goID {
						err := a.HBgraph.MakeEdge(prevN, rcvN) // create edge from Send node to Receive node
						var fromName, toName string
						if nGo == 0 {
							fromName = a.entryFn
						} else {
							fromName = a.goNames(a.RWIns[nGo][0].ins.(*ssa.Go))
						}
						if (*rcvN.Value).(goIns).goID == 0 {
							toName = a.entryFn
						} else {
							toName = a.goNames(a.RWIns[(*rcvN.Value).(goIns).goID][0].ins.(*ssa.Go))
						}
						if DEBUGHBGraph {
							log.Debug("Channel comm edge from Goroutine ", fromName, " [", nGo, "] to Goroutine ", toName, " [", (*rcvN.Value).(goIns).goID, "]")
						}
						if err != nil {
							log.Fatal(err)
						}
						err1 := a.HBgraph.MakeEdge(rcvN, prevN) // create edge from Receive node to Send node
						if err1 != nil {
							log.Fatal(err1)
						}
					}
				}
			} else if rcvIns, chR := anIns.(*ssa.UnOp); chR && channelComm {
				if ch := a.getRcvChan(rcvIns); ch != "" {
					if sndN, matching := chanSends[ch]; matching &&
						(*(sndN.Value)).(goIns).goID != (*(prevN.Value)).(goIns).goID {
						err := a.HBgraph.MakeEdge(sndN, prevN) // create edge from Send node to Receive node
						var fromName, toName string
						if (*sndN.Value).(goIns).goID == 0 {
							fromName = a.entryFn
						} else {
							fromName = a.goNames(a.RWIns[(*sndN.Value).(goIns).goID][0].ins.(*ssa.Go))
						}
						if nGo == 0 {
							toName = a.entryFn
						} else {
							toName = a.goNames(a.RWIns[nGo][0].ins.(*ssa.Go))
						}
						if DEBUGHBGraph {
							log.Debug("Channel comm edge from Goroutine ", fromName, " [", (*sndN.Value).(goIns).goID, "] to Goroutine ", toName, " [", nGo, "]")
						}
						if err != nil {
							log.Fatal(err)
						}
						err1 := a.HBgraph.MakeEdge(prevN, sndN) // create edge from Receive node to Send node
						if err1 != nil {
							log.Fatal(err1)
						}
					}
				}
			}
			if reIns, isReturn := anIns.(*ssa.Return); isReturn {
				if a.ifFnReturn[reIns.Parent()] == reIns { // this is final return
					for r, ifEndN := range ifSuccEndN {
						if r != len(ifSuccEndN)-1 {
							err := a.HBgraph.MakeEdge(ifEndN, prevN)
							if err != nil {
								log.Fatal(err)
							}
						}
					}
					ifSuccEndN = []graph.Node{} // reset slice containing last ins of each succ block preceeding final return
				}
			}
		}
	}
}
