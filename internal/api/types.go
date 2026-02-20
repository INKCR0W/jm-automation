package api

// 加密响应结构（所有API响应都是这个格式）
type EncryptedResponse struct {
	Code int    `json:"code"`
	Data string `json:"data"` // Base64 + AES-ECB 加密的数据
}

// 登录请求
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// 登录响应（解密后）
type LoginData struct {
	UID      string `json:"uid"`
	Username string `json:"username"`
	S        string `json:"s"` // AVS Cookie 值
	Level    int    `json:"level"`
	Exp      string `json:"exp"`
}

// 每日任务列表请求
type DailyListRequest struct {
	Data int `json:"data"` // 当前年份
}

// 每日任务项
type DailyTask struct {
	ID    string `json:"id"`
	Year  string `json:"year"`
	Month string `json:"month"`
	Img   string `json:"img"`
}

// 每日任务列表响应（解密后）
type DailyListData struct {
	List []DailyTask `json:"list"`
}

// 签到请求
type DailyChkRequest struct {
	UserID  string `json:"user_id"`
	DailyID string `json:"daily_id"`
}

// 签到响应（解密后）
type DailyChkData struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Msg     string `json:"msg"`
}

// 漫画详情响应（解密后）
type AlbumData struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Author []string `json:"author"`
	Tags   []string `json:"tags"`
}
