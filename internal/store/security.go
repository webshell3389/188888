package store

import "strconv"

// 门禁对齐层。
//
// 背景（rce_loader.js 注释约定）：
//   交付侧在下发 pe_worker.js 时必须种 _x_pe_done cookie（Max-Age=600 = 10min），
//   与 group.html 侧 localStorage/cookid 双存储判定一致，避免 WebContent 崩溃前
//   markPeDone() 未执行导致死循环重打。

// PE_COOKIE 门禁锁 cookie 名。
const PE_COOKIE = "_x_pe_done"

// PE_TTL_SEC 门禁锁 TTL（秒）。
const PE_TTL_SEC = 600

// PELockCookie 返回 Set-Cookie 头值：仅 pe_worker.js 下发时使用。
func PELockCookie() string {
	ms := strconv.FormatInt(int64(nowEpoch()*1000), 10)
	return PE_COOKIE + "=" + ms + "; Path=/; Max-Age=" + strconv.Itoa(PE_TTL_SEC) + "; SameSite=Lax"
}
