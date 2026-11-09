import {getQueryRowTarget} from './query-row-target';

const assert = {
    deepEqual(actual: unknown, expected: unknown) {
        if (JSON.stringify(actual) !== JSON.stringify(expected)) throw new Error(`Expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
    },
    equal(actual: unknown, expected: unknown, query: string) {
        if (actual !== expected) throw new Error(`Unexpected deletion target for ${query}`);
    },
};

{
    assert.deepEqual(getQueryRowTarget('SELECT * FROM paas_rad_52001.base_user LIMIT 100'), {schema: 'paas_rad_52001', table: 'base_user'});
    assert.deepEqual(getQueryRowTarget(' select * from `my``db`.`base_user` WHERE f_id = 42 ORDER BY f_id LIMIT 10; '), {schema: 'my`db', table: 'base_user'});
    assert.deepEqual(getQueryRowTarget('SELECT * FROM "public"."User"'), {schema: 'public', table: 'User'});
    assert.deepEqual(getQueryRowTarget('SELECT * FROM [dbo].[users]'), {schema: 'dbo', table: 'users'});
    assert.deepEqual(getQueryRowTarget('SELECT * FROM users'), {table: 'users'});
}

{
    for (const query of [
        'SELECT id FROM users',
        'SELECT count(*) FROM users',
        'SELECT * FROM users JOIN roles USING (id)',
        'SELECT * FROM users, roles',
        'SELECT * FROM users u',
        'SELECT * FROM users UNION SELECT * FROM other_users',
        'SELECT * FROM users GROUP BY id',
        'SELECT * FROM users; DELETE FROM users',
        'SELECT * FROM users /* comment */',
        'WITH users AS (SELECT * FROM other_users) SELECT * FROM users',
        'SELECT * FROM (SELECT * FROM users) derived',
        'SELECT * INTO backup FROM users',
        'DELETE FROM users',
    ]) assert.equal(getQueryRowTarget(query), undefined, query);
}
