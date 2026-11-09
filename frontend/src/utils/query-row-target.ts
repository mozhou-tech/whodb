const identifier = '(?:[A-Za-z_][\\w$]*|`(?:``|[^`])+`|"(?:""|[^"])+"|\\[(?:\\]\\]|[^\\]])+\\])';
const directTableQuery = new RegExp(`^SELECT\\s+\\*\\s+FROM\\s+(${identifier})(?:\\s*\\.\\s*(${identifier}))?(?=\\s|$)([\\s\\S]*)$`, 'i');

function unquoteIdentifier(value: string): string {
    if (value.startsWith('`')) return value.slice(1, -1).replace(/``/g, '`');
    if (value.startsWith('"')) return value.slice(1, -1).replace(/""/g, '"');
    if (value.startsWith('[')) return value.slice(1, -1).replace(/\]\]/g, ']');
    return value;
}

/** Resolves the source of a direct SELECT * query; complex results have no writable target. */
export function getQueryRowTarget(query: string): { schema?: string; table: string } | undefined {
    const sql = query.trim().replace(/;$/, '').trim();
    if (/;|--|\/\*|#|\b(?:JOIN|UNION|INTERSECT|EXCEPT|INTO|GROUP|HAVING|WINDOW|PROCEDURE)\b/i.test(sql)) return undefined;
    const match = sql.match(directTableQuery);
    if (!match) return undefined;
    const suffix = match[3].trim();
    if (suffix && !/^(?:WHERE|ORDER\s+BY|LIMIT|OFFSET|FETCH)\b/i.test(suffix)) return undefined;
    return match[2]
        ? { schema: unquoteIdentifier(match[1]), table: unquoteIdentifier(match[2]) }
        : { table: unquoteIdentifier(match[1]) };
}
