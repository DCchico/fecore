var http = require('http')
const sharp = require('sharp')
path = require('path')

function process(input_path, width, height) {
    sharp(input_path).resize(width, height).png().toFile("output.png");
};

http.createServer(function (req, res) {
    process("./input.jpg", 100, 100)
    res.writeHead(200, {'Content-Type': 'text/plain'});
    res.end('Hello World\n');
}).listen(8023, '0.0.0.0');

console.log('Server running at http://0.0.0.0:8023/');