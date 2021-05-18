package stats

import (
	log "github.com/sirupsen/logrus"
)

type SSAType int

const (
	// SSA instruction types.
	NCall SSAType = iota
	NChangeType
	NDefer
	NField
	NFieldAddr
	NFunction
	NGo
	NGlobal
	NLookup
	NMakeChan
	NMakeClosure
	NMakeInterface
	NSelect
	NSend
	NStore
	NUnOp
	NMapUpdate

	NLock
	NUnlock
	NChanRecv
	NWaitGroupWait
	NWaitGroupDone

	// Must be the last.
	NStatCount
)

var StatName map[SSAType]string = map[SSAType]string{
	NCall:          "Call Instructions",
	NChangeType:    "ChangeType Instructions",
	NDefer:         "Defer Instructions",
	NField:         "Field Instructions",
	NFieldAddr:     "FieldAddr Instructions",
	NFunction:      "Function Instructions",
	NGo:            "Go Instructions",
	NGlobal:        "Globals",
	NLookup:        "LookUp Instructions",
	NMakeChan:      "MakeChan Instructions",
	NMakeClosure:   "MakeClosure Instructions",
	NMakeInterface: "MakeInterface Instructions",
	NSelect:        "Select Instructions",
	NStore:         "Store Instructions",
	NUnOp:          "UnOP Instructions",
	NMapUpdate:     "MapUpdate Instructions",

	NLock:          "Lock Calls",
	NUnlock:        "Unlock Calls",
	NSend:          "Chan Send Instructions",
	NChanRecv:      "Chan Receive Instructions",
	NWaitGroupWait: "WaitGroup.Wait Calls",
	NWaitGroupDone: "WaitGroup.Done Calls",
}

var count map[SSAType]int = make(map[SSAType]int)

var CollectStats = false

func IncStat(whichStat SSAType) {
	if !CollectStats {
		return
	}
	_, ok := count[whichStat]
	if !ok {
		count[whichStat] = 0
	}
	count[whichStat]++
}

func GetStat(whichStat SSAType) int {
	return count[whichStat]
}

func ShowStats() {
	log.Info("------ STATS ------")
	for i := 0; i < int(NStatCount); i++ {
		stat := SSAType(i)
		log.Infof("  %-28s:%10d", StatName[stat], GetStat(stat))
	}
	log.Info("-------------------")
}
