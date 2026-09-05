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

		o = s.option(form.Flag, 'admin_http', _('允许明文 HTTP'), _('默认关闭。仅用于无法连接 HTTPS 的旧设备，启用后会持续显示安全警告。'));
		o.default = '0';
		o.rmempty = false;

		o = s.option(form.Value, 'port', _('后台端口'));
		o.datatype = 'port';
		o.default = '16601';
		o.rmempty = false;

		o = s.option(form.Value, 'confdir', _('配置目录'), _('存放配置、证书等运行数据'));
		o.default = '/etc/andey-proxy';
		o.rmempty = false;
		o.validate = function(sectionId, value) {
			var unsafe = [ '/', '/etc', '/usr', '/var', '/tmp', '/root', '/home' ];
			if (!value || value.charAt(0) !== '/' || unsafe.indexOf(value.replace(/\/+$/, '') || '/') !== -1)
				return _('请输入专用的绝对目录，不能使用系统根目录或顶级系统目录');
			return true;
		};

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
