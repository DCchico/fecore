#![deny(warnings)]

use std::convert::TryInto;
//use image::{self, io::Reader, DynamicImage};
//use std::path::Path;
use std::fs;
//use std::env;
use wasi_nn;

//use std::convert::Infallible;

use chrono::Utc;

//use sprintf::sprintf;

extern crate serde;

// async fn process_req(req: Request<Body>) -> Result<Response<Body>, Infallible> {
//     let req_time = Utc::now();
//     use std::time::Instant;
//     let fn_start = Instant::now();

//     let tp_val = "birdx224x224x3.rgb";
//     let _inference_result = run_model(tp_val).await;
//     //let elapsed = fn_start.elapsed().as_secs().to_string();
//     let elapsed = fn_start.elapsed().as_millis().to_string();
//     println!("Inference time elapsed: {}", elapsed);
//         //Ok(Response::new(Body::from(inference_result)))
//         /* Print time taken to perform inference */
//         //Ok(Response::new(Body::from(elapsed)))
//         /* Print timestamp when request was received */
//         Ok(Response::new(Body::from( req_time.to_rfc3339() )))
// }

// async fn shutdown_signal() {
//     // Wait for the CTRL+C signal
//     tokio::signal::ctrl_c()
//         .await
//         .expect("failed to install CTRL+C signal handler");
// }

pub fn main() {
    //let args: Vec<String> = env::args().collect();
    //let model_bin_name: &str = &args[1];
    //let tensor_path: &str = &args[1];
    let tensor_path: &str = "birdx224x224x3.rgb";

    // let weights = fs::read(model_bin_name).unwrap();
    // println!("Read graph weights, size in bytes: {}", weights.len());
    use std::time::Instant;
    let fn_start = Instant::now();

    let req_time = Utc::now();

    println!("Starting inference at {}", req_time);
    //let elapsed = fn_start.elapsed(); 
    // println!("Took {:.2?} s to bind to port 8080", elapsed);
    // println!("Bound at {:.2?}", Utc::now().timestamp_micros());

    //let tp_val = "birdx224x224x3.rgb";
    // let tp_str = image_name.to_str();
    // let _inference_result = run_model(tp_str).await;

    let tensor_height: u32 =  224;
    let tensor_width: i32  =  224;
    let weights: Vec<u8> = fs::read("lite-model_aiy_vision_classifier_birds_V1_3.tflite").unwrap();
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
    //return xs;

    //let elapsed = fn_start.elapsed().as_secs().to_string();
    let elapsed = fn_start.elapsed().as_millis().to_string();
    println!("Inference time elapsed: {}", elapsed);

}

// async fn run_model(tensor_path: &str) -> String {

//     let tensor_height: u32 =  224;
//     let tensor_width: i32  =  224;
//     let weights: Vec<u8> = fs::read("lite-model_aiy_vision_classifier_birds_V1_3.tflite").unwrap();
//     println!("Read graph weights, size in bytes: {}", weights.len());

//     let graph = unsafe {
//         wasi_nn::load(
//             &[&weights],
//             4, // encoding for tflite: wasi_nn::GRAPH_ENCODING_TENSORFLOWLITE
//             wasi_nn::EXECUTION_TARGET_CPU,
//         )
//         .unwrap()
//     };
//     println!("Loaded graph into wasi-nn with ID: {}", graph);

//     let context = unsafe { wasi_nn::init_execution_context(graph).unwrap() };
//     println!("Created wasi-nn execution context with ID: {}", context);

//     let tensor_data = fs::read(tensor_path).unwrap();
//     println!("Read tensor, size in bytes: {} (tensor_width={}, tensor_height={})", tensor_data.len(), tensor_width, tensor_height);
  
//     let tensor = wasi_nn::Tensor {
//         dimensions: &[1, tensor_height, tensor_height, 3],
//         r#type: 2,
//         data: &tensor_data,
//     };

//     unsafe {
//         wasi_nn::set_input(context, 0, tensor).unwrap();
//     }

//     // Execute the inference.
//     unsafe {
//         wasi_nn::compute(context).unwrap();
//     }
//     println!("Executed graph inference");

//     // Retrieve the output.
//     let mut output_buffer = vec![0u8; 965];
//     unsafe {
//         wasi_nn::get_output(
//             context,
//             0,
//             &mut output_buffer[..] as *mut [u8] as *mut u8,
//             output_buffer.len().try_into().unwrap(),
//         )
//         .unwrap();
//     }

//     let results = sort_results(&output_buffer);

//     let best_result = results[0].0;
//     let xs = serde_json::to_string(&best_result).unwrap();
//     println!("Inference results serialize into string {}", xs);
//     return xs;
// }

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
