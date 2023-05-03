#![deny(warnings)]

use std::convert::TryInto;
//use image::{self, io::Reader, DynamicImage};
//use std::path::Path;
use std::fs;
use wasi_nn;

use std::convert::Infallible;

use hyper::service::{make_service_fn, service_fn};
use hyper::{Body, Request, Response, Server};

extern crate serde;

async fn process_req(req: Request<Body>) -> Result<Response<Body>, Infallible> {
    let hdr = req.headers();
    if hdr.contains_key("tensor") { 
        let tp_val = hdr.get("tensor").unwrap();
        let tp_str = tp_val.to_str().unwrap();
        println!("Tensor Path: {}", tp_str);
        let inference_result = run_model(tp_str).await;
        Ok(Response::new(Body::from(inference_result)))
    } else {
        Ok(Response::new(Body::from("Please specify TENSOR NAME\n")))
    }
}

// async fn shutdown_signal() {
//     // Wait for the CTRL+C signal
//     tokio::signal::ctrl_c()
//         .await
//         .expect("failed to install CTRL+C signal handler");
// }

#[tokio::main(flavor = "current_thread")]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    // For every connection, we must make a `Service` to handle all
    // incoming HTTP requests on said connection.
    let make_svc = make_service_fn(|_conn| {
        // This is the `Service` that will handle the connection.
        // `service_fn` is a helper to convert a function that
        // returns a Response into a `Service`.
        async { Ok::<_, Infallible>(service_fn(process_req)) }
    });

    let addr = ([0, 0, 0, 0], 8080).into();

    let server = Server::bind(&addr).serve(make_svc);

    // let graceful = server.with_graceful_shutdown(shutdown_signal());

    println!("Listening on http://{}", addr);

    // graceful.await;
    server.await?;

    Ok(())
}

async fn run_model(tensor_path: &str) -> String {

    let tensor_height =  224;
    let tensor_width  =  224;
    let weights = fs::read("lite-model_aiy_vision_classifier_birds_V1_3.tflite").unwrap();
    println!("Read graph weights, size in bytes: {}", weights.len());

    let graph = unsafe {
        wasi_nn::load(
            &[&weights],
            4, // encoding for tflite: wasi_nn::GRAPH_ENCODING_TENSORFLOWLITE
            wasi_nn::EXECUTION_TARGET_CPU,
        )
        .unwrap()
    };
    println!("Loaded graph into wasi-nn with ID: {}", graph);

    let context = unsafe { wasi_nn::init_execution_context(graph).unwrap() };
    println!("Created wasi-nn execution context with ID: {}", context);

    let tensor_data = fs::read(tensor_path).unwrap();
    println!("Read tensor, size in bytes: {} (tensor_width={}, tensor_height={})", tensor_data.len(), tensor_width, tensor_height);
  
    let tensor = wasi_nn::Tensor {
        dimensions: &[1, tensor_height, tensor_height, 3],
        r#type: 2,
        data: &tensor_data,
    };

    unsafe {
        wasi_nn::set_input(context, 0, tensor).unwrap();
    }

    // Execute the inference.
    unsafe {
        wasi_nn::compute(context).unwrap();
    }
    println!("Executed graph inference");

    // Retrieve the output.
    let mut output_buffer = vec![0u8; 965];
    unsafe {
        wasi_nn::get_output(
            context,
            0,
            &mut output_buffer[..] as *mut [u8] as *mut u8,
            output_buffer.len().try_into().unwrap(),
        )
        .unwrap();
    }

    let results = sort_results(&output_buffer);

    let best_result = results[0].0;
    let xs = serde_json::to_string(&best_result).unwrap();
    println!("Inference results serialize into string {}", xs);
    return xs;
}

// Sort the buffer of probabilities. The graph places the match probability for each class at the
// index for that class (e.g. the probability of class 42 is placed at buffer[42]). Here we convert
// to a wrapping InferenceResult and sort the results. It is unclear why the MobileNet output
// indices are "off by one" but the `.skip(1)` below seems necessary to get results that make sense
// (e.g. 763 = "revolver" vs 762 = "restaurant")
fn sort_results(buffer: &[u8]) -> Vec<InferenceResult> {
    let mut results: Vec<InferenceResult> = buffer
        .iter()
        .skip(1)
        .enumerate()
        .map(|(c, p)| InferenceResult(c, (*p).into()))
        .collect();
    results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap());
    results
}

// A wrapper for class ID and match probabilities.
#[derive(Debug, PartialEq)]
struct InferenceResult(usize, f32);