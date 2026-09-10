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
		this.statusSpan.textContent = (running ? _('Running') : _('Stopped'))
			+ ' / ' + (enabled ? _('Enabled') : _('Disabled'));
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

		this.statusSpan = E('span', {}, _('Querying…'));

		var archEl = E('span', {}, _('Querying…'));
		var versionEl = E('span', {}, _('Querying…'));
		var passwordEl = E('span', {}, _('Querying…'));

		callBoard().then(function(board) {
			var parts = [];
			if (board.system)
				parts.push(board.system);
			if (board.release && board.release.target)
				parts.push(board.release.target);
			archEl.textContent = parts.join(' / ') || _('Unknown');
		}).catch(function() {
			archEl.textContent = _('Unavailable');
		});

		fs.read(confdir + '/version').then(function(content) {
			var ver = (content || '').trim();
			versionEl.textContent = ver || _('Unknown');
		}).catch(function() {
			versionEl.textContent = _('Unknown');
		});

		fs.read(confdir + '/initial-password').then(function(content) {
			var pwd = (content || '').trim();
			if (!pwd) {
				passwordEl.textContent = _('Not generated yet');
				return;
			}
			passwordEl.textContent = '';
			passwordEl.appendChild(E('div', {}, [
				E('code', { 'style': 'font-size:1.1em;background:#fff3cd;padding:.2em .6em;border:1px solid #e0c36c;border-radius:3px' }, pwd),
				E('div', { 'style': 'color:#c93c37;margin-top:.3em' },
					_('This is the initial password generated on first start (admin user "admin"); change it immediately after login. It will no longer be shown here once changed.'))
			]));
		}).catch(function() {
			passwordEl.textContent = _('Initial password changed or not generated yet');
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
			E('h3', {}, _('Runtime status')),
			row(_('Panel URL'), E('a', {
				'href': url,
				'target': '_blank',
				'rel': 'noopener noreferrer'
			}, url)),
			row(_('Service status'), E('span', {}, [
				this.statusSpan,
				' ',
				E('button', {
					'class': 'cbi-button cbi-button-apply',
					'click': function() {
						return callInitAction('andey-proxy', 'start').then(function() {
							ui.addNotification(null, E('p', _('andey-Proxy started')), 'info');
							return self.refreshStatus();
						});
					}
				}, _('Start')),
				' ',
				E('button', {
					'class': 'cbi-button cbi-button-negative',
					'click': function() {
						return callInitAction('andey-proxy', 'stop').then(function() {
							ui.addNotification(null, E('p', _('andey-Proxy stopped')), 'info');
							return self.refreshStatus();
						});
					}
				}, _('Stop')),
				enabled ? '' : E('span', { 'style': 'color:#c93c37;margin-left:.6em' },
					_('"Enabled" is not checked; save and apply first, then start the service.'))
			])),
			row(_('CPU architecture'), archEl),
			row(_('Version'), versionEl),
			row(_('Config files'), E('span', {}, [
				E('code', {}, '/etc/config/andey-proxy'),
				' / ',
				E('code', {}, confdir + '/config.json')
			])),
			row(_('Initial password'), passwordEl)
		]);
	},

	render: function() {
		var m, s, o;

		m = new form.Map('andey-proxy', 'andey-Proxy',
			_('All-in-one tool for DDNS / reverse proxy / ACME certificates / port forwarding. For detailed configuration use the "Admin Panel" page.'));

		s = m.section(form.NamedSection, 'main', 'andey-proxy', _('Basic settings'));
		s.addremove = false;

		o = s.option(form.Flag, 'enabled', _('Enable'), _('The service restarts automatically after save & apply.'));
		o.rmempty = false;

		o = s.option(form.Flag, 'admin_http', _('Allow plain HTTP'), _('Disabled by default. Only for legacy devices that cannot use HTTPS; a security warning is shown while enabled.'));
		o.default = '0';
		o.rmempty = false;

		o = s.option(form.Value, 'port', _('Admin port'));
		o.datatype = 'port';
		o.default = '16606';
		o.rmempty = false;

		o = s.option(form.Value, 'confdir', _('Config directory'), _('Stores configuration, certificates and other runtime data.'));
		o.default = '/etc/andey-proxy';
		o.rmempty = false;
		o.validate = function(sectionId, value) {
			var v = (value || '').replace(/\/+$/, '');
			if (v.indexOf('/etc/') !== 0 || v.indexOf('..') !== -1)
				return _('The config directory must be a dedicated subdirectory under /etc/ and must not contain ..');
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
				ui.addNotification(null, E('p', _('andey-Proxy service restarted')), 'info');
				return self.refreshStatus();
			});
	}
});
