package httpd_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/render"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"

	"github.com/drakkan/sftpgo/config"
	"github.com/drakkan/sftpgo/dataprovider"
	"github.com/drakkan/sftpgo/httpd"
	"github.com/drakkan/sftpgo/logger"
	"github.com/drakkan/sftpgo/sftpd"
	"github.com/drakkan/sftpgo/utils"
)

const (
	defaultUsername       = "test_user"
	defaultPassword       = "test_password"
	testPubKey            = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC03jj0D+djk7pxIf/0OhrxrchJTRZklofJ1NoIu4752Sq02mdXmarMVsqJ1cAjV5LBVy3D1F5U6XW4rppkXeVtd04Pxb09ehtH0pRRPaoHHlALiJt8CoMpbKYMA8b3KXPPriGxgGomvtU2T2RMURSwOZbMtpsugfjYSWenyYX+VORYhylWnSXL961LTyC21ehd6d6QnW9G7E5hYMITMY9TuQZz3bROYzXiTsgN0+g6Hn7exFQp50p45StUMfV/SftCMdCxlxuyGny2CrN/vfjO7xxOo2uv7q1qm10Q46KPWJQv+pgZ/OfL+EDjy07n5QVSKHlbx+2nT4Q0EgOSQaCTYwn3YjtABfIxWwgAFdyj6YlPulCL22qU4MYhDcA6PSBwDdf8hvxBfvsiHdM+JcSHvv8/VeJhk6CmnZxGY0fxBupov27z3yEO8nAg8k+6PaUiW1MSUfuGMF/ktB8LOstXsEPXSszuyXiOv4DaryOXUiSn7bmRqKcEFlJusO6aZP0= nicola@p1"
	logSender             = "APITesting"
	userPath              = "/api/v1/user"
	activeConnectionsPath = "/api/v1/connection"
	quotaScanPath         = "/api/v1/quota_scan"
	versionPath           = "/api/v1/version"
	metricsPath           = "/metrics"
	webBasePath           = "/web"
	webUsersPath          = "/web/users"
	webUserPath           = "/web/user"
	webConnectionsPath    = "/web/connections"
	configDir             = ".."
)

var (
	defaultPerms       = []string{dataprovider.PermAny}
	homeBasePath       string
	testServer         *httptest.Server
	providerDriverName string
)

func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		homeBasePath = "C:\\"
	} else {
		homeBasePath = "/tmp"
	}
	logfilePath := filepath.Join(configDir, "sftpgo_api_test.log")
	logger.InitLogger(logfilePath, 5, 1, 28, false, zerolog.DebugLevel)
	config.LoadConfig(configDir, "")
	providerConf := config.GetProviderConf()
	providerDriverName = providerConf.Driver

	err := dataprovider.Initialize(providerConf, configDir)
	if err != nil {
		logger.Warn(logSender, "", "error initializing data provider: %v", err)
		os.Exit(1)
	}
	dataProvider := dataprovider.GetProvider()
	httpdConf := config.GetHTTPDConfig()

	httpdConf.BindPort = 8081
	httpd.SetBaseURL("http://127.0.0.1:8081")

	sftpd.SetDataProvider(dataProvider)
	httpd.SetDataProvider(dataProvider)

	go func() {
		go func() {
			if err := httpdConf.Initialize(configDir); err != nil {
				logger.Error(logSender, "", "could not start HTTP server: %v", err)
			}
		}()
	}()

	waitTCPListening(fmt.Sprintf("%s:%d", httpdConf.BindAddress, httpdConf.BindPort))

	testServer = httptest.NewServer(httpd.GetHTTPRouter())
	defer testServer.Close()

	exitCode := m.Run()
	os.Remove(logfilePath)
	os.Exit(exitCode)
}

func TestBasicUserHandling(t *testing.T) {
	user, _, err := httpd.AddUser(getTestUser(), http.StatusOK)
	if err != nil {
		t.Errorf("unable to add user: %v", err)
	}
	user.MaxSessions = 10
	user.QuotaSize = 4096
	user.QuotaFiles = 2
	user.UploadBandwidth = 128
	user.DownloadBandwidth = 64
	user, _, err = httpd.UpdateUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to update user: %v", err)
	}
	users, _, err := httpd.GetUsers(0, 0, defaultUsername, http.StatusOK)
	if err != nil {
		t.Errorf("unable to get users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("number of users mismatch, expected: 1, actual: %v", len(users))
	}
	_, err = httpd.RemoveUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to remove: %v", err)
	}
}

func TestAddUserNoCredentials(t *testing.T) {
	u := getTestUser()
	u.Password = ""
	u.PublicKeys = []string{}
	_, _, err := httpd.AddUser(u, http.StatusBadRequest)
	if err != nil {
		t.Errorf("unexpected error adding user with no credentials: %v", err)
	}
}

func TestAddUserNoUsername(t *testing.T) {
	u := getTestUser()
	u.Username = ""
	_, _, err := httpd.AddUser(u, http.StatusBadRequest)
	if err != nil {
		t.Errorf("unexpected error adding user with no home dir: %v", err)
	}
}

func TestAddUserNoHomeDir(t *testing.T) {
	u := getTestUser()
	u.HomeDir = ""
	_, _, err := httpd.AddUser(u, http.StatusBadRequest)
	if err != nil {
		t.Errorf("unexpected error adding user with no home dir: %v", err)
	}
}

func TestAddUserInvalidHomeDir(t *testing.T) {
	u := getTestUser()
	u.HomeDir = "relative_path"
	_, _, err := httpd.AddUser(u, http.StatusBadRequest)
	if err != nil {
		t.Errorf("unexpected error adding user with invalid home dir: %v", err)
	}
}

func TestAddUserNoPerms(t *testing.T) {
	u := getTestUser()
	u.Permissions = []string{}
	_, _, err := httpd.AddUser(u, http.StatusBadRequest)
	if err != nil {
		t.Errorf("unexpected error adding user with no perms: %v", err)
	}
}

func TestAddUserInvalidPerms(t *testing.T) {
	u := getTestUser()
	u.Permissions = []string{"invalidPerm"}
	_, _, err := httpd.AddUser(u, http.StatusBadRequest)
	if err != nil {
		t.Errorf("unexpected error adding user with no perms: %v", err)
	}
}

func TestUserPublicKey(t *testing.T) {
	u := getTestUser()
	invalidPubKey := "invalid"
	validPubKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC03jj0D+djk7pxIf/0OhrxrchJTRZklofJ1NoIu4752Sq02mdXmarMVsqJ1cAjV5LBVy3D1F5U6XW4rppkXeVtd04Pxb09ehtH0pRRPaoHHlALiJt8CoMpbKYMA8b3KXPPriGxgGomvtU2T2RMURSwOZbMtpsugfjYSWenyYX+VORYhylWnSXL961LTyC21ehd6d6QnW9G7E5hYMITMY9TuQZz3bROYzXiTsgN0+g6Hn7exFQp50p45StUMfV/SftCMdCxlxuyGny2CrN/vfjO7xxOo2uv7q1qm10Q46KPWJQv+pgZ/OfL+EDjy07n5QVSKHlbx+2nT4Q0EgOSQaCTYwn3YjtABfIxWwgAFdyj6YlPulCL22qU4MYhDcA6PSBwDdf8hvxBfvsiHdM+JcSHvv8/VeJhk6CmnZxGY0fxBupov27z3yEO8nAg8k+6PaUiW1MSUfuGMF/ktB8LOstXsEPXSszuyXiOv4DaryOXUiSn7bmRqKcEFlJusO6aZP0= nicola@p1"
	u.PublicKeys = []string{invalidPubKey}
	_, _, err := httpd.AddUser(u, http.StatusBadRequest)
	if err != nil {
		t.Errorf("unexpected error adding user with invalid pub key: %v", err)
	}
	u.PublicKeys = []string{validPubKey}
	user, _, err := httpd.AddUser(u, http.StatusOK)
	if err != nil {
		t.Errorf("unable to add user: %v", err)
	}
	user.PublicKeys = []string{validPubKey, invalidPubKey}
	_, _, err = httpd.UpdateUser(user, http.StatusBadRequest)
	if err != nil {
		t.Errorf("update user with invalid public key must fail: %v", err)
	}
	user.PublicKeys = []string{validPubKey, validPubKey, validPubKey}
	_, _, err = httpd.UpdateUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to update user: %v", err)
	}
	_, err = httpd.RemoveUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to remove: %v", err)
	}
}

func TestUpdateUser(t *testing.T) {
	user, _, err := httpd.AddUser(getTestUser(), http.StatusOK)
	if err != nil {
		t.Errorf("unable to add user: %v", err)
	}
	user.HomeDir = filepath.Join(homeBasePath, "testmod")
	user.UID = 33
	user.GID = 101
	user.MaxSessions = 10
	user.QuotaSize = 4096
	user.QuotaFiles = 2
	user.Permissions = []string{dataprovider.PermCreateDirs, dataprovider.PermDelete, dataprovider.PermDownload}
	user.UploadBandwidth = 1024
	user.DownloadBandwidth = 512
	user, _, err = httpd.UpdateUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to update user: %v", err)
	}
	_, err = httpd.RemoveUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to remove: %v", err)
	}
}

func TestUpdateUserNoCredentials(t *testing.T) {
	user, _, err := httpd.AddUser(getTestUser(), http.StatusOK)
	if err != nil {
		t.Errorf("unable to add user: %v", err)
	}
	user.Password = ""
	user.PublicKeys = []string{}
	// password and public key will be omitted from json serialization if empty and so they will remain unchanged
	// and no validation error will be raised
	_, _, err = httpd.UpdateUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unexpected error updating user with no credentials: %v", err)
	}
	_, err = httpd.RemoveUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to remove: %v", err)
	}
}

func TestUpdateUserEmptyHomeDir(t *testing.T) {
	user, _, err := httpd.AddUser(getTestUser(), http.StatusOK)
	if err != nil {
		t.Errorf("unable to add user: %v", err)
	}
	user.HomeDir = ""
	_, _, err = httpd.UpdateUser(user, http.StatusBadRequest)
	if err != nil {
		t.Errorf("unexpected error updating user with empty home dir: %v", err)
	}
	_, err = httpd.RemoveUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to remove: %v", err)
	}
}

func TestUpdateUserInvalidHomeDir(t *testing.T) {
	user, _, err := httpd.AddUser(getTestUser(), http.StatusOK)
	if err != nil {
		t.Errorf("unable to add user: %v", err)
	}
	user.HomeDir = "relative_path"
	_, _, err = httpd.UpdateUser(user, http.StatusBadRequest)
	if err != nil {
		t.Errorf("unexpected error updating user with empty home dir: %v", err)
	}
	_, err = httpd.RemoveUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to remove: %v", err)
	}
}

func TestUpdateNonExistentUser(t *testing.T) {
	_, _, err := httpd.UpdateUser(getTestUser(), http.StatusNotFound)
	if err != nil {
		t.Errorf("unable to update user: %v", err)
	}
}

func TestGetNonExistentUser(t *testing.T) {
	_, _, err := httpd.GetUserByID(0, http.StatusNotFound)
	if err != nil {
		t.Errorf("unable to get user: %v", err)
	}
}

func TestDeleteNonExistentUser(t *testing.T) {
	_, err := httpd.RemoveUser(getTestUser(), http.StatusNotFound)
	if err != nil {
		t.Errorf("unable to remove user: %v", err)
	}
}

func TestAddDuplicateUser(t *testing.T) {
	user, _, err := httpd.AddUser(getTestUser(), http.StatusOK)
	if err != nil {
		t.Errorf("unable to add user: %v", err)
	}
	_, _, err = httpd.AddUser(getTestUser(), http.StatusInternalServerError)
	if err != nil {
		t.Errorf("unable to add second user: %v", err)
	}
	_, _, err = httpd.AddUser(getTestUser(), http.StatusOK)
	if err == nil {
		t.Errorf("adding a duplicate user must fail")
	}
	_, err = httpd.RemoveUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to remove user: %v", err)
	}
}

func TestGetUsers(t *testing.T) {
	user1, _, err := httpd.AddUser(getTestUser(), http.StatusOK)
	if err != nil {
		t.Errorf("unable to add user: %v", err)
	}
	u := getTestUser()
	u.Username = defaultUsername + "1"
	user2, _, err := httpd.AddUser(u, http.StatusOK)
	if err != nil {
		t.Errorf("unable to add second user: %v", err)
	}
	users, _, err := httpd.GetUsers(0, 0, "", http.StatusOK)
	if err != nil {
		t.Errorf("unable to get users: %v", err)
	}
	if len(users) < 2 {
		t.Errorf("at least 2 users are expected")
	}
	users, _, err = httpd.GetUsers(1, 0, "", http.StatusOK)
	if err != nil {
		t.Errorf("unable to get users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("1 user is expected")
	}
	users, _, err = httpd.GetUsers(1, 1, "", http.StatusOK)
	if err != nil {
		t.Errorf("unable to get users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("1 user is expected")
	}
	_, _, err = httpd.GetUsers(1, 1, "", http.StatusInternalServerError)
	if err == nil {
		t.Errorf("get users must succeed, we requested a fail for a good request")
	}
	_, err = httpd.RemoveUser(user1, http.StatusOK)
	if err != nil {
		t.Errorf("unable to remove user: %v", err)
	}
	_, err = httpd.RemoveUser(user2, http.StatusOK)
	if err != nil {
		t.Errorf("unable to remove user: %v", err)
	}
}

func TestGetQuotaScans(t *testing.T) {
	_, _, err := httpd.GetQuotaScans(http.StatusOK)
	if err != nil {
		t.Errorf("unable to get quota scans: %v", err)
	}
	_, _, err = httpd.GetQuotaScans(http.StatusInternalServerError)
	if err == nil {
		t.Errorf("quota scan request must succeed, we requested to check a wrong status code")
	}
}

func TestStartQuotaScan(t *testing.T) {
	user, _, err := httpd.AddUser(getTestUser(), http.StatusOK)
	if err != nil {
		t.Errorf("unable to add user: %v", err)
	}
	_, err = httpd.StartQuotaScan(user, http.StatusCreated)
	if err != nil {
		t.Errorf("unable to start quota scan: %v", err)
	}
	_, err = httpd.RemoveUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to remove user: %v", err)
	}
}

func TestGetVersion(t *testing.T) {
	_, _, err := httpd.GetVersion(http.StatusOK)
	if err != nil {
		t.Errorf("unable to get sftp version: %v", err)
	}
	_, _, err = httpd.GetVersion(http.StatusInternalServerError)
	if err == nil {
		t.Errorf("get version request must succeed, we requested to check a wrong status code")
	}
}

func TestGetConnections(t *testing.T) {
	_, _, err := httpd.GetConnections(http.StatusOK)
	if err != nil {
		t.Errorf("unable to get sftp connections: %v", err)
	}
	_, _, err = httpd.GetConnections(http.StatusInternalServerError)
	if err == nil {
		t.Errorf("get sftp connections request must succeed, we requested to check a wrong status code")
	}
}

func TestCloseActiveConnection(t *testing.T) {
	_, err := httpd.CloseConnection("non_existent_id", http.StatusNotFound)
	if err != nil {
		t.Errorf("unexpected error closing non existent sftp connection: %v", err)
	}
}

func TestUserBaseDir(t *testing.T) {
	dataProvider := dataprovider.GetProvider()
	dataprovider.Close(dataProvider)
	config.LoadConfig(configDir, "")
	providerConf := config.GetProviderConf()
	providerConf.UsersBaseDir = homeBasePath
	err := dataprovider.Initialize(providerConf, configDir)
	if err != nil {
		t.Errorf("error initializing data provider with users base dir")
	}
	httpd.SetDataProvider(dataprovider.GetProvider())
	u := getTestUser()
	u.HomeDir = ""
	user, _, err := httpd.AddUser(getTestUser(), http.StatusOK)
	if err != nil {
		t.Errorf("unable to add user: %v", err)
	}
	if user.HomeDir != filepath.Join(providerConf.UsersBaseDir, u.Username) {
		t.Errorf("invalid home dir: %v", user.HomeDir)
	}
	_, err = httpd.RemoveUser(user, http.StatusOK)
	if err != nil {
		t.Errorf("unable to remove: %v", err)
	}
	dataProvider = dataprovider.GetProvider()
	dataprovider.Close(dataProvider)
	config.LoadConfig(configDir, "")
	providerConf = config.GetProviderConf()
	err = dataprovider.Initialize(providerConf, configDir)
	if err != nil {
		t.Errorf("error initializing data provider")
	}
	httpd.SetDataProvider(dataprovider.GetProvider())
	sftpd.SetDataProvider(dataprovider.GetProvider())
}

func TestProviderErrors(t *testing.T) {
	if providerDriverName == dataprovider.BoltDataProviderName {
		t.Skip("skipping test provider errors for bolt provider")
	}
	dataProvider := dataprovider.GetProvider()
	dataprovider.Close(dataProvider)
	_, _, err := httpd.GetUserByID(0, http.StatusInternalServerError)
	if err != nil {
		t.Errorf("get user with provider closed must fail: %v", err)
	}
	_, _, err = httpd.GetUsers(1, 0, defaultUsername, http.StatusInternalServerError)
	if err != nil {
		t.Errorf("get users with provider closed must fail: %v", err)
	}
	_, _, err = httpd.UpdateUser(dataprovider.User{}, http.StatusInternalServerError)
	if err != nil {
		t.Errorf("update user with provider closed must fail: %v", err)
	}
	_, err = httpd.RemoveUser(dataprovider.User{}, http.StatusInternalServerError)
	if err != nil {
		t.Errorf("delete user with provider closed must fail: %v", err)
	}
	config.LoadConfig(configDir, "")
	providerConf := config.GetProviderConf()
	err = dataprovider.Initialize(providerConf, configDir)
	if err != nil {
		t.Errorf("error initializing data provider")
	}
	httpd.SetDataProvider(dataprovider.GetProvider())
	sftpd.SetDataProvider(dataprovider.GetProvider())
}

// test using mock http server

func TestBasicUserHandlingMock(t *testing.T) {
	user := getTestUser()
	userAsJSON := getUserAsJSON(t, user)
	req, _ := http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(userAsJSON))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	err := render.DecodeJSON(rr.Body, &user)
	if err != nil {
		t.Errorf("Error get user: %v", err)
	}
	req, _ = http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(userAsJSON))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusInternalServerError, rr.Code)
	user.MaxSessions = 10
	user.UploadBandwidth = 128
	user.Permissions = []string{dataprovider.PermAny, dataprovider.PermDelete, dataprovider.PermDownload}
	userAsJSON = getUserAsJSON(t, user)
	req, _ = http.NewRequest(http.MethodPut, userPath+"/"+strconv.FormatInt(user.ID, 10), bytes.NewBuffer(userAsJSON))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)

	req, _ = http.NewRequest(http.MethodGet, userPath+"/"+strconv.FormatInt(user.ID, 10), nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)

	var updatedUser dataprovider.User
	err = render.DecodeJSON(rr.Body, &updatedUser)
	if err != nil {
		t.Errorf("Error decoding updated user: %v", err)
	}
	if user.MaxSessions != updatedUser.MaxSessions || user.UploadBandwidth != updatedUser.UploadBandwidth {
		t.Errorf("Error modifying user actual: %v, %v", updatedUser.MaxSessions, updatedUser.UploadBandwidth)
	}
	if len(updatedUser.Permissions) != 1 {
		t.Errorf("permissions other than any should be removed")
	}
	if !utils.IsStringInSlice(dataprovider.PermAny, updatedUser.Permissions) {
		t.Errorf("permissions mismatch")
	}
	req, _ = http.NewRequest(http.MethodDelete, userPath+"/"+strconv.FormatInt(user.ID, 10), nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestGetUserByIdInvalidParamsMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, userPath+"/0", nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusNotFound, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, userPath+"/a", nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
}

func TestAddUserNoUsernameMock(t *testing.T) {
	user := getTestUser()
	user.Username = ""
	userAsJSON := getUserAsJSON(t, user)
	req, _ := http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(userAsJSON))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
}

func TestAddUserInvalidHomeDirMock(t *testing.T) {
	user := getTestUser()
	user.HomeDir = "relative_path"
	userAsJSON := getUserAsJSON(t, user)
	req, _ := http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(userAsJSON))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
}

func TestAddUserInvalidPermsMock(t *testing.T) {
	user := getTestUser()
	user.Permissions = []string{}
	userAsJSON := getUserAsJSON(t, user)
	req, _ := http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(userAsJSON))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
}

func TestAddUserInvalidJsonMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer([]byte("invalid json")))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
}

func TestUpdateUserInvalidJsonMock(t *testing.T) {
	user := getTestUser()
	userAsJSON := getUserAsJSON(t, user)
	req, _ := http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(userAsJSON))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	err := render.DecodeJSON(rr.Body, &user)
	if err != nil {
		t.Errorf("Error get user: %v", err)
	}
	req, _ = http.NewRequest(http.MethodPut, userPath+"/"+strconv.FormatInt(user.ID, 10), bytes.NewBuffer([]byte("Invalid json")))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
	req, _ = http.NewRequest(http.MethodDelete, userPath+"/"+strconv.FormatInt(user.ID, 10), nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestUpdateUserInvalidParamsMock(t *testing.T) {
	user := getTestUser()
	userAsJSON := getUserAsJSON(t, user)
	req, _ := http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(userAsJSON))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	err := render.DecodeJSON(rr.Body, &user)
	if err != nil {
		t.Errorf("Error get user: %v", err)
	}
	user.HomeDir = ""
	userAsJSON = getUserAsJSON(t, user)
	req, _ = http.NewRequest(http.MethodPut, userPath+"/"+strconv.FormatInt(user.ID, 10), bytes.NewBuffer(userAsJSON))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
	userID := user.ID
	user.ID = 0
	userAsJSON = getUserAsJSON(t, user)
	req, _ = http.NewRequest(http.MethodPut, userPath+"/"+strconv.FormatInt(userID, 10), bytes.NewBuffer(userAsJSON))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
	user.ID = userID
	req, _ = http.NewRequest(http.MethodPut, userPath+"/0", bytes.NewBuffer(userAsJSON))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusNotFound, rr.Code)
	req, _ = http.NewRequest(http.MethodPut, userPath+"/a", bytes.NewBuffer(userAsJSON))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
	req, _ = http.NewRequest(http.MethodDelete, userPath+"/"+strconv.FormatInt(user.ID, 10), nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestGetUsersMock(t *testing.T) {
	user := getTestUser()
	userAsJSON := getUserAsJSON(t, user)
	req, _ := http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(userAsJSON))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	err := render.DecodeJSON(rr.Body, &user)
	if err != nil {
		t.Errorf("Error get user: %v", err)
	}
	req, _ = http.NewRequest(http.MethodGet, userPath+"?limit=510&offset=0&order=ASC&username="+defaultUsername, nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	var users []dataprovider.User
	err = render.DecodeJSON(rr.Body, &users)
	if err != nil {
		t.Errorf("Error decoding users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("1 user is expected")
	}
	req, _ = http.NewRequest(http.MethodGet, userPath+"?limit=a&offset=0&order=ASC", nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, userPath+"?limit=1&offset=a&order=ASC", nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, userPath+"?limit=1&offset=0&order=ASCa", nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)

	req, _ = http.NewRequest(http.MethodDelete, userPath+"/"+strconv.FormatInt(user.ID, 10), nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestDeleteUserInvalidParamsMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodDelete, userPath+"/0", nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusNotFound, rr.Code)
	req, _ = http.NewRequest(http.MethodDelete, userPath+"/a", nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
}

func TestGetQuotaScansMock(t *testing.T) {
	req, err := http.NewRequest("GET", quotaScanPath, nil)
	if err != nil {
		t.Errorf("error get quota scan: %v", err)
	}
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestStartQuotaScanMock(t *testing.T) {
	user := getTestUser()
	userAsJSON := getUserAsJSON(t, user)
	req, _ := http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(userAsJSON))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	err := render.DecodeJSON(rr.Body, &user)
	if err != nil {
		t.Errorf("Error get user: %v", err)
	}
	_, err = os.Stat(user.HomeDir)
	if err == nil {
		os.Remove(user.HomeDir)
	}
	// simulate a duplicate quota scan
	userAsJSON = getUserAsJSON(t, user)
	sftpd.AddQuotaScan(user.Username)
	req, _ = http.NewRequest(http.MethodPost, quotaScanPath, bytes.NewBuffer(userAsJSON))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusConflict, rr.Code)
	sftpd.RemoveQuotaScan(user.Username)

	userAsJSON = getUserAsJSON(t, user)
	req, _ = http.NewRequest(http.MethodPost, quotaScanPath, bytes.NewBuffer(userAsJSON))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusCreated, rr.Code)

	req, _ = http.NewRequest(http.MethodGet, quotaScanPath, nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	var scans []sftpd.ActiveQuotaScan
	err = render.DecodeJSON(rr.Body, &scans)
	if err != nil {
		t.Errorf("Error get active scans: %v", err)
	}
	for len(scans) > 0 {
		req, _ = http.NewRequest(http.MethodGet, quotaScanPath, nil)
		rr = executeRequest(req)
		checkResponseCode(t, http.StatusOK, rr.Code)
		err = render.DecodeJSON(rr.Body, &scans)
		if err != nil {
			t.Errorf("Error get active scans: %v", err)
			break
		}
	}
	_, err = os.Stat(user.HomeDir)
	if err != nil && os.IsNotExist(err) {
		os.MkdirAll(user.HomeDir, 0777)
	}
	req, _ = http.NewRequest(http.MethodPost, quotaScanPath, bytes.NewBuffer(userAsJSON))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusCreated, rr.Code)
	req, _ = http.NewRequest(http.MethodDelete, userPath+"/"+strconv.FormatInt(user.ID, 10), nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestStartQuotaScanBadUserMock(t *testing.T) {
	user := getTestUser()
	userAsJSON := getUserAsJSON(t, user)
	req, _ := http.NewRequest(http.MethodPost, quotaScanPath, bytes.NewBuffer(userAsJSON))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusNotFound, rr.Code)
}

func TestStartQuotaScanNonExistentUserMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, quotaScanPath, bytes.NewBuffer([]byte("invalid json")))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
}

func TestGetVersionMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, versionPath, nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestGetConnectionsMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, activeConnectionsPath, nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestDeleteActiveConnectionMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodDelete, activeConnectionsPath+"/connectionID", nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusNotFound, rr.Code)
}

func TestNotFoundMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/non/existing/path", nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusNotFound, rr.Code)
}

func TestMethodNotAllowedMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, activeConnectionsPath, nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestMetricsMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, metricsPath, nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestGetWebRootMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusMovedPermanently, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, webBasePath, nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusMovedPermanently, rr.Code)
}

func TestBasicWebUsersMock(t *testing.T) {
	user := getTestUser()
	userAsJSON := getUserAsJSON(t, user)
	req, _ := http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(userAsJSON))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	err := render.DecodeJSON(rr.Body, &user)
	if err != nil {
		t.Errorf("Error get user: %v", err)
	}
	user1 := getTestUser()
	user1.Username += "1"
	user1AsJSON := getUserAsJSON(t, user1)
	req, _ = http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(user1AsJSON))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	err = render.DecodeJSON(rr.Body, &user1)
	if err != nil {
		t.Errorf("Error get user1: %v", err)
	}
	req, _ = http.NewRequest(http.MethodGet, webUsersPath, nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, webUsersPath+"?qlimit=a", nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, webUsersPath+"?qlimit=1", nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, webUserPath, nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, webUserPath+"/"+strconv.FormatInt(user.ID, 10), nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, webUserPath+"/0", nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusNotFound, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, webUserPath+"/a", nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
	form := make(url.Values)
	form.Set("username", user.Username)
	req, _ = http.NewRequest(http.MethodPost, webUserPath, strings.NewReader(form.Encode()))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	req, _ = http.NewRequest(http.MethodPost, webUserPath+"/"+strconv.FormatInt(user.ID, 10), strings.NewReader(form.Encode()))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	req, _ = http.NewRequest(http.MethodPost, webUserPath+"/0", strings.NewReader(form.Encode()))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusNotFound, rr.Code)
	req, _ = http.NewRequest(http.MethodPost, webUserPath+"/a", strings.NewReader(form.Encode()))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusBadRequest, rr.Code)
	req, _ = http.NewRequest(http.MethodDelete, userPath+"/"+strconv.FormatInt(user.ID, 10), nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	req, _ = http.NewRequest(http.MethodDelete, userPath+"/"+strconv.FormatInt(user1.ID, 10), nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestWebUserAddMock(t *testing.T) {
	user := getTestUser()
	user.UploadBandwidth = 32
	user.DownloadBandwidth = 64
	user.UID = 1000
	form := make(url.Values)
	form.Set("username", user.Username)
	form.Set("home_dir", user.HomeDir)
	form.Set("password", user.Password)
	form.Set("permissions", "*")
	// test invalid url escape
	req, _ := http.NewRequest(http.MethodPost, webUserPath+"?a=%2", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	form.Set("public_keys", testPubKey)
	form.Set("uid", strconv.FormatInt(int64(user.UID), 10))
	form.Set("gid", "a")
	// test invalid gid
	req, _ = http.NewRequest(http.MethodPost, webUserPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	form.Set("gid", "0")
	form.Set("max_sessions", "a")
	// test invalid max sessions
	req, _ = http.NewRequest(http.MethodPost, webUserPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	form.Set("max_sessions", "0")
	form.Set("quota_size", "a")
	// test invalid quota size
	req, _ = http.NewRequest(http.MethodPost, webUserPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	form.Set("quota_size", "0")
	form.Set("quota_files", "a")
	// test invalid quota files
	req, _ = http.NewRequest(http.MethodPost, webUserPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	form.Set("quota_files", "0")
	form.Set("upload_bandwidth", "a")
	// test invalid upload bandwidth
	req, _ = http.NewRequest(http.MethodPost, webUserPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	form.Set("upload_bandwidth", strconv.FormatInt(user.UploadBandwidth, 10))
	form.Set("download_bandwidth", "a")
	// test invalid download bandwidth
	req, _ = http.NewRequest(http.MethodPost, webUserPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	form.Set("download_bandwidth", strconv.FormatInt(user.DownloadBandwidth, 10))
	req, _ = http.NewRequest(http.MethodPost, webUserPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusSeeOther, rr.Code)
	// the user already exists, was created with the above request
	req, _ = http.NewRequest(http.MethodPost, webUserPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, userPath+"?limit=1&offset=0&order=ASC&username="+user.Username, nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	var users []dataprovider.User
	err := render.DecodeJSON(rr.Body, &users)
	if err != nil {
		t.Errorf("Error decoding users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("1 user is expected")
	}
	newUser := users[0]
	if newUser.UID != user.UID {
		t.Errorf("uid does not match")
	}
	if newUser.UploadBandwidth != user.UploadBandwidth {
		t.Errorf("upload_bandwidth does not match")
	}
	if newUser.DownloadBandwidth != user.DownloadBandwidth {
		t.Errorf("download_bandwidth does not match")
	}
	if !utils.IsStringInSlice(testPubKey, newUser.PublicKeys) {
		t.Errorf("public_keys does not match")
	}
	req, _ = http.NewRequest(http.MethodDelete, userPath+"/"+strconv.FormatInt(newUser.ID, 10), nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestWebUserUpdateMock(t *testing.T) {
	user := getTestUser()
	userAsJSON := getUserAsJSON(t, user)
	req, _ := http.NewRequest(http.MethodPost, userPath, bytes.NewBuffer(userAsJSON))
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	err := render.DecodeJSON(rr.Body, &user)
	if err != nil {
		t.Errorf("Error get user: %v", err)
	}
	user.MaxSessions = 1
	user.QuotaFiles = 2
	user.QuotaSize = 3
	user.GID = 1000
	form := make(url.Values)
	form.Set("username", user.Username)
	form.Set("home_dir", user.HomeDir)
	form.Set("uid", "0")
	form.Set("gid", strconv.FormatInt(int64(user.GID), 10))
	form.Set("max_sessions", strconv.FormatInt(int64(user.MaxSessions), 10))
	form.Set("quota_size", strconv.FormatInt(user.QuotaSize, 10))
	form.Set("quota_files", strconv.FormatInt(int64(user.QuotaFiles), 10))
	form.Set("upload_bandwidth", "0")
	form.Set("download_bandwidth", "0")
	form.Set("permissions", "*")
	req, _ = http.NewRequest(http.MethodPost, webUserPath+"/"+strconv.FormatInt(user.ID, 10), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusSeeOther, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, userPath+"?limit=1&offset=0&order=ASC&username="+user.Username, nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
	var users []dataprovider.User
	err = render.DecodeJSON(rr.Body, &users)
	if err != nil {
		t.Errorf("Error decoding users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("1 user is expected")
	}
	updateUser := users[0]
	if user.HomeDir != updateUser.HomeDir {
		t.Errorf("home dir does not match")
	}
	if user.MaxSessions != updateUser.MaxSessions {
		t.Errorf("max_sessions does not match")
	}
	if user.QuotaFiles != updateUser.QuotaFiles {
		t.Errorf("quota_files does not match")
	}
	if user.QuotaSize != updateUser.QuotaSize {
		t.Errorf("quota_size does not match")
	}
	if user.GID != updateUser.GID {
		t.Errorf("gid does not match")
	}
	req, _ = http.NewRequest(http.MethodDelete, userPath+"/"+strconv.FormatInt(user.ID, 10), nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestProviderClosedMock(t *testing.T) {
	if providerDriverName == dataprovider.BoltDataProviderName {
		t.Skip("skipping test provider errors for bolt provider")
	}
	dataProvider := dataprovider.GetProvider()
	dataprovider.Close(dataProvider)
	req, _ := http.NewRequest(http.MethodGet, webUsersPath, nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusInternalServerError, rr.Code)
	req, _ = http.NewRequest(http.MethodGet, webUserPath+"/0", nil)
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusInternalServerError, rr.Code)
	form := make(url.Values)
	form.Set("username", "test")
	req, _ = http.NewRequest(http.MethodPost, webUserPath+"/0", strings.NewReader(form.Encode()))
	rr = executeRequest(req)
	checkResponseCode(t, http.StatusInternalServerError, rr.Code)
	config.LoadConfig(configDir, "")
	providerConf := config.GetProviderConf()
	err := dataprovider.Initialize(providerConf, configDir)
	if err != nil {
		t.Errorf("error initializing data provider")
	}
	httpd.SetDataProvider(dataprovider.GetProvider())
	sftpd.SetDataProvider(dataprovider.GetProvider())
}

func TestGetWebConnectionsMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, webConnectionsPath, nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func TestStaticFilesMock(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/static/favicon.ico", nil)
	rr := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rr.Code)
}

func waitTCPListening(address string) {
	for {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			logger.WarnToConsole("tcp server %v not listening: %v\n", address, err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		logger.InfoToConsole("tcp server %v now listening\n", address)
		defer conn.Close()
		break
	}
}

func getTestUser() dataprovider.User {
	return dataprovider.User{
		Username:    defaultUsername,
		Password:    defaultPassword,
		HomeDir:     filepath.Join(homeBasePath, defaultUsername),
		Permissions: defaultPerms,
	}
}

func getUserAsJSON(t *testing.T, user dataprovider.User) []byte {
	json, err := json.Marshal(user)
	if err != nil {
		t.Errorf("error get user as json: %v", err)
		return []byte("{}")
	}
	return json
}

func executeRequest(req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	testServer.Config.Handler.ServeHTTP(rr, req)
	return rr
}

func checkResponseCode(t *testing.T, expected, actual int) {
	if expected != actual {
		t.Errorf("Expected response code %d. Got %d", expected, actual)
	}
}
