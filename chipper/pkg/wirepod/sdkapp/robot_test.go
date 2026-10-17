package sdkapp

import (
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
)

func useBotInfo(t *testing.T, info vars.RobotInfoStore, cached []Robot) {
	t.Helper()
	oldInfo, oldRobots := vars.BotInfo, robots
	t.Cleanup(func() { vars.BotInfo, robots = oldInfo, oldRobots })
	vars.BotInfo, robots = info, cached
}

func botInfo(esn, guid string) vars.RobotInfoStore {
	info := vars.RobotInfoStore{GlobalGUID: "global-guid"}
	info.Robots = append(info.Robots, struct {
		Esn       string `json:"esn"`
		IPAddress string `json:"ip_address"`
		GUID      string `json:"guid"`
		Activated bool   `json:"activated"`
	}{Esn: esn, IPAddress: "192.0.2.1", GUID: guid, Activated: true})
	return info
}

func TestCachedConnectionIsReusedWhileTheGUIDMatches(t *testing.T) {
	useBotInfo(t, botInfo("0dd1b652", "same-guid"), []Robot{{ESN: "0dd1b652", GUID: "same-guid"}})

	robot, index, ok := cachedRobot("0DD1B652")

	if !ok || index != 0 || robot.GUID != "same-guid" {
		t.Fatalf("cachedRobot = %+v, %d, %v; want the cached connection", robot, index, ok)
	}
}

func TestCachedConnectionIsDroppedWhenTheRobotGotANewGUID(t *testing.T) {
	// Setting the robot up again gives it a new GUID; the old connection now gets 401 invalid token.
	useBotInfo(t, botInfo("0dd1b652", "new-guid"), []Robot{{ESN: "0dd1b652", GUID: "old-guid"}, {ESN: "00e20145", GUID: "x"}})

	_, _, ok := cachedRobot("0dd1b652")

	if ok {
		t.Fatal("a connection with the old GUID was reused")
	}
	if len(robots) != 1 || robots[0].ESN != "00e20145" {
		t.Fatalf("robots after dropping the stale one: %+v", robots)
	}
}

func TestEmptyGUIDMeansTheGlobalGUID(t *testing.T) {
	useBotInfo(t, botInfo("0dd1b652", ""), []Robot{{ESN: "0dd1b652", GUID: "global-guid"}})

	if _, _, ok := cachedRobot("0dd1b652"); !ok {
		t.Fatal("a connection made with the global GUID was dropped")
	}
}
