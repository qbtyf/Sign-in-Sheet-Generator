package server

// V2.0 多语种：后端错误消息错误码映射。
// writeErr 返回 {error: 中文原文, code: "err.xxx", detail: 剥离前缀后的动态部分}，
// 前端按语言包把 code 翻译成当前语言（词条里的 {msg}/{ext}/{f} 用 detail 填充）；
// 未命中映射的消息保持原文返回（前端原样显示，不影响功能）。
import "strings"

type errRule struct {
	prefix string // 中文消息前缀（空串不参与）
	code   string // 对应语言包词条键
}

// errRules 错误码规则表：长的前缀放前面，避免被短前缀抢先命中
var errRules = []errRule{
	{"暂不支持旧版 .doc/.xls 格式", "err.oldFormat"},
	{"暂不支持旧版 .doc/.xls", "err.tplOldFormat"},
	{"不支持的文件格式：", "err.unsupportedFmt"},
	{"不支持的模板格式：", "err.tplFmt"},
	{"保存文件失败: ", "err.saveFail"},
	{"请求格式错误", "err.badRequest"},
	{"未收到文件", "err.noFile"},
	{"内置模板不存在", "err.builtinNotFound"},
	{"非法模板名", "err.badTplName"},
	{"尚未选择模板", "err.noTpl"},
	{"请先选择模板", "err.noTpl"},
	{"模板文件不存在", "err.tplFileMissing"},
	{"请填写模板名称", "err.needTplName"},
	{"当前没有已加载的模板", "err.noLoadedTpl"},
	{"保存失败: ", "err.saveOnly"},
	{"名单为空：", "err.rosterEmpty"},
	{"该分类暂无生成文件", "err.noGroupOutputs"},
	{"暂无生成文件", "err.noOutputs"},
	{"文件不存在", "err.fileMissing"},
	{"模板表格定位失败", "err.tplLocate"},
	{"模板中未找到含「姓名」表头行", "err.noNameHeader"},
	{"Excel 模板中未找到含「姓名」的表头行", "err.noNameHeaderXls"},
	{"打开 Excel 模板失败: ", "err.openXlsTpl"},
	{"打开 Excel 失败: ", "err.openExcel"},
	{"读取 Word 失败: ", "err.openWord"},
	{"表格序号越界", "err.sheetIndex"},
	{"读取模板失败: ", "err.openTpl"},
	{"打开 docx 失败: ", "err.openDocx"},
	{"打开源 docx 失败: ", "err.openSrcDocx"},
	{"解析 docx 失败: ", "err.parseDocx"},
	{"docx 内未找到 ", "err.noElement"},
	{"读取模板配置失败: ", "err.readTplConf"},
	{"模板配置解析失败", "err.parseTplConf"},
	{"生成 ", "err.genFail"},
}

// matchErr 按前缀匹配错误码；返回码与剥离前缀后的动态部分
func matchErr(msg string) (code, detail string) {
	for _, r := range errRules {
		if strings.HasPrefix(msg, r.prefix) {
			return r.code, strings.TrimPrefix(msg, r.prefix)
		}
	}
	return "", ""
}
