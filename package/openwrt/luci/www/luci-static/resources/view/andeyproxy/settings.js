'use strict';
'require view';
'require form';
'require rpc';
'require ui';
'require uci';
'require fs';
'require poll';

var callInitAction = rpc.declare({
	object: 'luci',
	method: 'setInitAction',
	params: [ 'name', 'action' ],
	expect: { result: false }
});

var callInitList = rpc.declare({
	object: 'luci',
	method: 'getInitList',
	params: [ 'name' ],
	expect: { '': {} }
});

var callBoard = rpc.declare({
	object: 'system',
	method: 'board',
	expect: { '': {} }
});

var callServiceList = rpc.declare({
	object: 'service',
	method: 'list',
	params: [ 'name' ],
	expect: { '': {} }
});

function panelURL() {
	var port = uci.get('andey-proxy', 'main', 'port') || '16606';
	var scheme = uci.get('andey-proxy', 'main', 'admin_http') === '1' ? 'http' : 'https';
	var host = window.location.hostname;
	if (host.indexOf(':') !== -1)
		host = '[' + host + ']';
	return scheme + '://' + host + ':' + port + '/';
}

return view.extend({
	statusSpan: null,

	updateStatus: function(initRes, svcRes) {
		if (!this.statusSpan)
			return;
		var info = (initRes && initRes['andey-proxy']) || initRes || {};
		var svc = (svcRes && svcRes['andey-proxy']) || {};
		var running = !!info.running;
		if (!running && svc.instances)
			for (var k in svc.instances)
				if (svc.instances[k] && svc.instances[k].running) {
					running = true;
					break;
				}
		var enabled = !!info.enabled;
		this.statusSpan.textContent = (running ? _('运行中') : _('未运行'))
			+ ' / ' + (enabled ? _('已启用') : _('未启用'));
		this.statusSpan.style.color = running ? '#2ea043' : '#c93c37';
	},

	refreshStatus: function() {
		var self = this;
		return Promise.all([
			callInitList('andey-proxy').catch(function() { return null; }),
			callServiceList('andey-proxy').catch(function() { return null; })
		]).then(function(results) {
			self.updateStatus(results[0], results[1]);
		});
	},

	renderStatus: function() {
		var self = this;
		var confdir = (uci.get('andey-proxy', 'main', 'confdir') || '/etc/andey-proxy').replace(/\/+$/, '');
		var enabled = uci.get('andey-proxy', 'main', 'enabled') === '1';
		var url = panelURL();

		this.statusSpan = E('span', {}, _('查询中…'));

		var archEl = E('span', {}, _('查询中…'));
		var versionEl = E('span', {}, _('查询中…'));
		var passwordEl = E('span', {}, _('查询中…'));

		callBoard().then(function(board) {
			var parts = [];
			if (board.system)
				parts.push(board.system);
			if (board.release && board.release.target)
				parts.push(board.release.target);
			archEl.textContent = parts.join(' / ') || _('未知');
		}).catch(function() {
			archEl.textContent = _('无法获取');
		});

		fs.exec('/usr/bin/andey-proxy', [ '-v' ]).then(function(res) {
			var out = (res.stdout || '').trim();
			versionEl.textContent = res.code === 0 && out ? out : _('未知');
		}).catch(function() {
			versionEl.textContent = _('无法获取');
		});

		fs.read(confdir + '/initial-password').then(function(content) {
			var pwd = (content || '').trim();
			if (!pwd) {
				passwordEl.textContent = _('尚未生成');
				return;
			}
			passwordEl.textContent = '';
			passwordEl.appendChild(E('div', {}, [
				E('code', { 'style': 'font-size:1.1em;background:#fff3cd;padding:.2em .6em;border:1px solid #e0c36c;border-radius:3px' }, pwd),
				E('div', { 'style': 'color:#c93c37;margin-top:.3em' },
					_('这是首次启动生成的初始密码（管理员 admin），登录后请立即修改；修改后此处不再显示。'))
			]));
		}).catch(function() {
			passwordEl.textContent = _('已修改初始密码或尚未生成');
		});

		poll.add(L.bind(this.refreshStatus, this), 5);
		this.refreshStatus();

		var row = function(label, content) {
			return E('div', { 'class': 'cbi-value', 'style': 'margin-bottom:.4em' }, [
				E('label', { 'class': 'cbi-value-title', 'style': 'display:inline-block;min-width:8em;font-weight:bold' }, label),
				E('div', { 'class': 'cbi-value-field', 'style': 'display:inline-block' }, content)
			]);
		};

		return E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, _('运行状态')),
			row(_('面板地址'), E('a', {
				'href': url,
				'target': '_blank',
				'rel': 'noopener noreferrer'
			}, url)),
			row(_('服务状态'), E('span', {}, [
				this.statusSpan,
				' ',
				E('button', {
					'class': 'cbi-button cbi-button-apply',
					'click': function() {
						return callInitAction('andey-proxy', 'start').then(function() {
							ui.addNotification(null, E('p', _('andey-Proxy 已启动')), 'info');
							return self.refreshStatus();
						});
					}
				}, _('启动')),
				' ',
				E('button', {
					'class': 'cbi-button cbi-button-negative',
					'click': function() {
						return callInitAction('andey-proxy', 'stop').then(function() {
							ui.addNotification(null, E('p', _('andey-Proxy 已停止')), 'info');
							return self.refreshStatus();
						});
					}
				}, _('停止')),
				enabled ? '' : E('span', { 'style': 'color:#c93c37;margin-left:.6em' },
					_('未勾选“启用”，请先保存并应用后再启动'))
			])),
			row(_('CPU 架构'), archEl),
			row(_('版本'), versionEl),
			row(_('配置文件'), E('span', {}, [
				E('code', {}, '/etc/config/andey-proxy'),
				' / ',
				E('code', {}, confdir + '/config.json')
			])),
			row(_('初始密码'), passwordEl)
		]);
	},

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
		o.default = '16606';
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

		var self = this;
		return uci.load('andey-proxy').then(function() {
			return m.render().then(function(formEl) {
				return E('div', {}, [ self.renderStatus(), formEl ]);
			});
		});
	},

	handleSaveApply: function(ev) {
		var self = this;
		return this.super('handleSaveApply', [ ev ])
			.then(function() {
				return callInitAction('andey-proxy', 'restart');
			})
			.then(function() {
				ui.addNotification(null, E('p', _('andey-Proxy 服务已重启')), 'info');
				return self.refreshStatus();
			});
	}
});
