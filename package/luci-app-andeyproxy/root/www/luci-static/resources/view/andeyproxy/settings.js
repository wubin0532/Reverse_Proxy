'use strict';
'require view';
'require form';
'require rpc';
'require ui';

var callInitAction = rpc.declare({
	object: 'luci',
	method: 'setInitAction',
	params: [ 'name', 'action' ],
	expect: { result: false }
});

return view.extend({
	render: function() {
		var m, s, o;

		m = new form.Map('andey-proxy', 'andey-Proxy',
			'DDNS / 反向代理 / ACME 证书 / 端口转发一体工具。详细配置请使用左侧“管理面板”。');

		s = m.section(form.NamedSection, 'main', 'andey-proxy', _('基本设置'));
		s.addremove = false;

		o = s.option(form.Flag, 'enabled', _('启用'), _('保存并应用后自动重启服务'));
		o.rmempty = false;

		o = s.option(form.Value, 'port', _('后台端口'));
		o.datatype = 'port';
		o.default = '16601';
		o.rmempty = false;

		o = s.option(form.Value, 'confdir', _('配置目录'), _('存放配置、证书等运行数据'));
		o.default = '/etc/andey-proxy';
		o.rmempty = false;

		return m.render();
	},

	handleSaveApply: function(ev) {
		return this.super('handleSaveApply', [ ev ])
			.then(function() {
				return callInitAction('andey-proxy', 'restart');
			})
			.then(function() {
				ui.addNotification(null, E('p', _('andey-Proxy 服务已重启')), 'info');
			});
	}
});
