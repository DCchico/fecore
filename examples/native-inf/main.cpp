#include <iostream>
#include <string>
#include <fstream>
#include <boost/beast/core.hpp>
#include <boost/beast/http.hpp>
#include <boost/beast/version.hpp>
#include <boost/asio.hpp>

#include "tensorflow/lite/interpreter.h"
#include "tensorflow/lite/model.h"

using tcp = boost::asio::ip::tcp;
namespace http = boost::beast::http;

class RequestHandler {
public:

    std::string run_inf(std::string image_tensor_path) {
        try {
            // Read the model file and allocate the interpreter
            std::unique_ptr<tflite::FlatBufferModel> model = tflite::FlatBufferModel::BuildFromFile("lite-model_aiy_vision_classifier_birds_V1_3.tflite");
            tflite::InterpreterBuilder builder(*model);
            std::unique_ptr<tflite::Interpreter> interpreter;
            builder(&interpreter);
            interpreter->AllocateTensors();

            // Read the input tensor data from the specified file path
            std::ifstream input_file(image_tensor_path, std::ios::binary);
            std::vector<char> input_data((std::istreambuf_iterator<char>(input_file)), std::istreambuf_iterator<char>());

            // Set the input tensor data
            TfLiteTensor* input_tensor = interpreter->tensor(interpreter->inputs()[0]);
            memcpy(input_tensor->data.raw, input_data.data(), input_data.size());

            // Run the model
            interpreter->Invoke();

            std::vector<float> output_data = tflite::outputTensor<float>(interpreter, 0);
            
            return "SUCCESS";

        } catch (const std::exception& e) {
            // Handle any exceptions and print an error message
            std::cerr << "Error: " << e.what() << std::endl;
            return "INFERENCE ERROR";
        }
    }

    void operator()(const http::request<http::string_body>& req, http::response<http::string_body>& res) {
        try {
            // Get the image tensor path from the request query string
            std::string image_tensor_path = req.target().to_string().substr(1);

            std::cout<<image_tensor_path<<std::endl;

            std::string out = run_inf(image_tensor_path);

            // Construct the response body with the image tensor data
            res.result(http::status::ok);
            res.set(http::field::content_type, "text/plain");
            res.set(http::field::access_control_allow_origin, "*");
            res.body() = out;
        } catch (const std::exception& e) {
            // Handle any exceptions and set the response status and body accordingly
            res.result(http::status::internal_server_error);
            res.set(http::field::content_type, "text/plain");
            res.set(http::field::access_control_allow_origin, "*");
            res.body() = e.what();
        }
    }
};

int main() {
    try {
        // Create an io_context and an acceptor for incoming connections
        boost::asio::io_context io_context;
        tcp::acceptor acceptor(io_context, tcp::endpoint(tcp::v4(), 8080));

        // Run the server
        while (true) {
            // Wait for an incoming connection and accept it
            tcp::socket socket(io_context);
            acceptor.accept(socket);

            // Read the incoming request
            boost::beast::flat_buffer buffer;
            http::request<http::string_body> request;
            http::read(socket, buffer, request);

            // Handle the request and construct the response
            http::response<http::string_body> response;
            RequestHandler()(request, response);

            // Send the response back to the client
            http::write(socket, response);
        }
    } catch (const std::exception& e) {
        std::cerr << "Error: " << e.what() << std::endl;
        return 1;
    }

    return 0;
}
