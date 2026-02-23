package api

// API 相关常量

const (
	// APP 版本
	AppVersion = "2.0.13"

	// 默认域名（会自动更新）
	DefaultBaseURL = "https://www.cdnaspa.vip"

	// API 路径
	PathLogin          = "/login"
	PathAlbum          = "/album"
	PathChapter        = "/chapter"
	PathFavorite       = "/favorite"
	PathFavoriteFolder = "/favorite_folder"
	PathSearch         = "/search"
	PathLike           = "/like"
	PathForum          = "/forum"
	PathDailyList      = "/daily_list/filter"
	PathDailyChk       = "/daily_chk"
	PathPromote        = "/promote"
	PathPromoteList    = "/promote_list"
	PathLatest         = "/latest"
	PathSerialization  = "/serialization"
	PathCategories     = "/categories/filter"

	// 请求头
	HeaderUserAgent      = "User-Agent"
	HeaderToken          = "token"
	HeaderTokenParam     = "tokenparam"
	HeaderAcceptEncoding = "Accept-Encoding"
	HeaderCookie         = "Cookie"

	// User-Agent（移动端）
	UserAgentMobile = "Mozilla/5.0 (Linux; Android 9; V1938CT Build/PQ3A.190705.11211812; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/91.0.4472.114 Safari/537.36"
)

// 响应码
const (
	CodeSuccess = 200
	CodeError   = -1
)

// API 域名列表（会自动更新）
var DomainAPIList = []string{
	"www.cdnaspa.vip",
	"www.cdnaspa.club",
	"www.cdnplaystation6.vip",
	"www.cdnplaystation6.cc",
}

// 图片 CDN 域名列表
var DomainImageList = []string{
	"cdn-msp.jmapiproxy1.cc",
	"cdn-msp.jmapiproxy2.cc",
	"cdn-msp2.jmapiproxy2.cc",
	"cdn-msp3.jmapiproxy2.cc",
	"cdn-msp.jmapinodeudzn.net",
	"cdn-msp3.jmapinodeudzn.net",
}
