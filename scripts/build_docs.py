"""Render the committed public reference bundle offline. Never calls a provider."""
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
bundle = json.loads((root / 'internal/crmcli/reference/wecom.json').read_text())
destination = root / 'docs/wecom'
destination.mkdir(exist_ok=True)


def cell(value):
    return str(value).replace('|', '\\|').replace('\n', '<br>')


lines = ['# 企微官方文档目录与覆盖', '',
         '来源为企微官方导航。仅标记“已取得参数表/示例”的条目有离线正文摘取；其余只有目录元数据。', '',
         '字段、代码示例都是资料，不能作为授权、CRM 已接入或真实 API 验收证明。完整授权前提、段落限制及最新内容须核对原文。', '',
         '| 文档 ID | 官方主题 | 离线资料 |', '|---|---|---|']
for doc in bundle['documents']:
    link = f"[{cell(doc['title'])}]({doc['source_url']})"
    if doc['status'] == 'official_body_retrieved':
        coverage = f"[已取得参数表/示例](wecom/{doc['id']}.md)"
        body = [f"# {doc['title']}", '', f"[企微官方原文]({doc['source_url']})", '',
                f"核对日期：{doc['retrieved_on']}；官方 doc_id：{doc['doc_id']}；原始页面 SHA-256：`{doc['content_sha256']}`。", '',
                '本页仅整理官方参数表、示例及接口地址。机械提取不等于逐字段人工核验；未提取的叙述、权限前提、适用版本和调用限制仍以官方原文为准。示例身份和凭据是资料占位，不用于实际调用。', '',
                '适用导航：', '']
        body += ['- ' + ' → '.join(a['hierarchy']) + f"（{a['id']}）" for a in doc['aliases']]
        if doc.get('request_urls'):
            body += ['', '## 官方接口地址（资料）', ''] + ['- `' + u.replace('`', '') + '`' for u in doc['request_urls']]
        for i, table in enumerate(doc.get('tables', []), 1):
            body += ['', f"## 参数/说明表 {i}" + (f"：{table['context']}" if table['context'] else ''), '']
            width = max([len(table['columns'])] + [len(r) for r in table['rows']])
            columns = table['columns'] + ['官方未命名列'] * (width - len(table['columns']))
            body += ['| ' + ' | '.join(map(cell, columns)) + ' |', '| ' + ' | '.join(['---'] * width) + ' |']
            body += ['| ' + ' | '.join(map(cell, row + [''] * (width - len(row)))) + ' |' for row in table['rows']]
        for i, example in enumerate(doc.get('examples', []), 1):
            fence = '`' * max(3, max([len(part) for part in __import__('re').findall(r'`+', example)] or [0]) + 1)
            body += ['', f'## 官方代码示例 {i}（仅供阅读）', '', fence, example, fence]
        # Cosmetic Markdown normalization; the JSON bundle retains the extracted
        # source example bytes for inspection, not execution or request signing.
        (destination / (doc['id'] + '.md')).write_text('\n'.join(line.expandtabs(4).rstrip() for line in '\n'.join(body).splitlines()) + '\n')
    else:
        coverage = '正文未取得；禁止推测字段'
    lines.append(f"| {doc['id']} | {link} | {coverage} |")
(root / 'docs/wecom-index.md').write_text('\n'.join(lines) + '\n')
(root / 'docs/wecom-coverage.json').write_text(json.dumps(bundle['coverage'], ensure_ascii=False, indent=2) + '\n')
print(f"rendered {bundle['coverage']['body_retrieved']} official references; {bundle['coverage']['body_unavailable']} explicit gaps")
