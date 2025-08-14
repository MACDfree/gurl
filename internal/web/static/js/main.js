document.addEventListener('DOMContentLoaded', function() {
	const requestContent = document.getElementById('request-content');
	const runButton = document.getElementById('run-request');
	const responseContent = document.getElementById('response-content');
	const examplesList = document.getElementById('examples-list');

	// 加载示例请求
	fetch('/api/examples')
		.then(response => response.json())
		.then(data => {
			if (data.status === 'success') {
				data.examples.forEach(example => {
					const exampleItem = document.createElement('div');
					exampleItem.className = 'example-item';
					exampleItem.textContent = example;
					exampleItem.addEventListener('click', () => loadExample(example));
					examplesList.appendChild(exampleItem);
				});
			}
		})
		.catch(error => {
			console.error('Failed to load examples:', error);
		});

	// 运行请求
	runButton.addEventListener('click', function() {
		const content = requestContent.value;
		if (!content.trim()) {
			alert('Please enter a request.');
			return;
		}

		responseContent.textContent = 'Loading...';

		fetch('/api/request', {
			method: 'POST',
			headers: {
				'Content-Type': 'text/plain'
			},
			body: content
		})
			.then(response => response.json())
			.then(data => {
				responseContent.textContent = JSON.stringify(data, null, 2);
			})
			.catch(error => {
				responseContent.textContent = `Error: ${error.message}`;
			});
	});

	// 加载示例
	function loadExample(exampleName) {
		// 这里应该加载示例文件内容
		// 为简化，我们只是显示一个提示
		alert(`Loading example: ${exampleName}`);
	}
});